package metrics

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/runner"
)

func TestParseVolumes(t *testing.T) {
	got := ParseVolumes(fixture(t, "docker/df-volumes.json"))
	// shop: 60.42MB + 65.31MB + 1.443GB + 4.533GB + 758B + 89B (and three 0B);
	// ledger: 10.8GB + 625.2MB + 7.257MB + 262.9kB + 289kB + 418B + 131B. The
	// anonymous volumes and pgdata (no compose label) belong to no project.
	want := map[string]int64{"shop": 6101730847, "ledger": 11433009449}
	if len(got) != len(want) || got["shop"] != want["shop"] || got["ledger"] != want["ledger"] {
		t.Fatalf("ParseVolumes = %v, want %v", got, want)
	}

	for in, want := range map[string]map[string]int64{
		`[]`:   {},
		`null`: {},
		`[{"Labels":"com.docker.compose.project=a","Size":"1.5kB"},{"Labels":"x=1,com.docker.compose.project=a","Size":"2B"}]`: {"a": 1502},
		`[{"Labels":"com.docker.compose.project=a","Size":"N/A"},{"Labels":"com.docker.compose.project=a","Size":"-1B"}]`:      {},
		`[{"Labels":"com.docker.compose.project=","Size":"1B"},{"Labels":"com.docker.compose.projects=b","Size":"1B"}]`:        {},
		`[{"Labels":"com.docker.compose.project=a","Size":"1.2TB"}]`:                                                           {"a": 1200000000000},
	} {
		got := ParseVolumes([]byte(in))
		if got == nil || len(got) != len(want) {
			t.Errorf("ParseVolumes(%s) = %v, want %v", in, got, want)
			continue
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("ParseVolumes(%s)[%s] = %d, want %d", in, k, got[k], v)
			}
		}
	}
	if got := ParseVolumes([]byte("{not json")); got != nil {
		t.Errorf("ParseVolumes(bad json) = %v, want nil", got)
	}
}

// blocks is the allocated size of path as WalkFolder counts it.
func blocks(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Blocks * 512
}

func writeBytes(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWalkFolderCapSymlinkUnreadable(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeBytes(t, filepath.Join(root, "a.bin"), 64<<10)
	writeBytes(t, filepath.Join(root, "sub", "b.bin"), 128<<10)
	writeBytes(t, filepath.Join(root, "sub", "deep", "c.bin"), 8<<10)
	// A big tree outside, reached only through a symlink, is never counted.
	outside := t.TempDir()
	writeBytes(t, filepath.Join(outside, "huge.bin"), 4<<20)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	// A hard link to a.bin is counted once.
	if err := os.Link(filepath.Join(root, "a.bin"), filepath.Join(root, "sub", "a-again.bin")); err != nil {
		t.Fatal(err)
	}
	var want int64
	for _, p := range []string{"", "a.bin", "sub", "sub/b.bin", "sub/deep", "sub/deep/c.bin", "link"} {
		want += blocks(t, filepath.Join(root, p))
	}

	got, partial := WalkFolder(ctx, root, 500000, time.Minute)
	if got != want || partial {
		t.Fatalf("WalkFolder = %d, partial %v; want %d, false", got, partial, want)
	}
	if got >= 4<<20 {
		t.Fatalf("WalkFolder = %d: the symlinked tree was followed", got)
	}

	t.Run("entry cap", func(t *testing.T) {
		got, partial := WalkFolder(ctx, root, 3, time.Minute)
		if !partial || got <= 0 || got >= want {
			t.Fatalf("WalkFolder capped at 3 entries = %d, partial %v; want 0 < n < %d, true", got, partial, want)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		if _, partial := WalkFolder(ctx, root, 500000, -time.Second); !partial {
			t.Fatal("a walk past its deadline is not partial")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, partial := WalkFolder(cctx, root, 500000, time.Minute); !partial {
			t.Fatal("a cancelled walk is not partial")
		}
	})
	t.Run("unreadable folder", func(t *testing.T) {
		// Root bypasses permissions, so the folder read fails through the seam.
		readDir := func(name string) ([]os.DirEntry, error) {
			if name == filepath.Join(root, "sub") {
				return nil, fs.ErrPermission
			}
			return os.ReadDir(name)
		}
		got, partial, err := walkFolder(ctx, root, 500000, time.Minute, readDir)
		if err != nil {
			t.Fatal(err)
		}
		skipped := blocks(t, filepath.Join(root, "sub", "b.bin")) + blocks(t, filepath.Join(root, "sub", "deep")) +
			blocks(t, filepath.Join(root, "sub", "deep", "c.bin"))
		if !partial || got != want-skipped {
			t.Fatalf("walk with sub unreadable = %d, partial %v; want %d, true", got, partial, want-skipped)
		}
	})
	t.Run("missing root", func(t *testing.T) {
		got, partial, err := walkFolder(ctx, filepath.Join(root, "gone"), 500000, time.Minute, os.ReadDir)
		if err == nil || got != 0 || !partial {
			t.Fatalf("walk of a missing root = %d, %v, %v; want 0, true and an error", got, partial, err)
		}
		if got, partial := WalkFolder(ctx, filepath.Join(root, "gone"), 500000, time.Minute); got != 0 || !partial {
			t.Fatalf("WalkFolder of a missing root = %d, %v; want 0, true", got, partial)
		}
	})
}

// fakeAfter stands in for time.After in the disk loop: each wait is recorded
// and returns when the test fires it.
type fakeAfter struct {
	mu    sync.Mutex
	waits []time.Duration
	chans []chan time.Time
}

func (f *fakeAfter) after(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := make(chan time.Time, 1)
	f.waits = append(f.waits, d)
	f.chans = append(f.chans, c)
	return c
}

func (f *fakeAfter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waits)
}

// fire ends wait i (0-based) and returns its duration.
func (f *fakeAfter) fire(t *testing.T, i int) time.Duration {
	t.Helper()
	waitFor(t, func() bool { return f.count() > i })
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chans[i] <- time.Time{}
	return f.waits[i]
}

func TestDiskUsageHourlyOneAtATime(t *testing.T) {
	e := newSamplerEnv(t)
	m := New(Options{Store: e.st, Cmd: e.cmd, Source: e.src, Host: Host{Proc: e.proc}, Now: e.clock.now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	fa := &fakeAfter{}
	m.after = fa.after
	e.cmd.set(runner.DockerDF, string(fixture(t, "docker/df-volumes.json")), nil)

	var mu sync.Mutex
	var walked []string
	inFlight, peak := 0, 0
	sizes := map[string]int64{"shop": 4096, "notes": 8192}
	failWalk := map[string]bool{}
	m.walk = func(_ context.Context, root string) (int64, bool, error) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		name := filepath.Base(root)
		walked = append(walked, name)
		n, fail := sizes[name], failWalk[name]
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		if fail {
			return 0, true, errors.New("walk failed")
		}
		return n, name == "notes", nil
	}
	ctx := context.Background()
	m.Start(ctx)
	t.Cleanup(m.Close)

	if d := fa.fire(t, 0); d != 2*time.Minute {
		t.Fatalf("first disk usage read after %s, want 2m", d)
	}
	waitFor(t, func() bool { return fa.count() >= 2 }) // the run ended and the next wait began
	first := e.clock.now()
	got := m.Disk()
	wantShop := DiskUsage{Volumes: 6101730847, Folder: 4096, At: first}
	wantNotes := DiskUsage{Volumes: 0, Folder: 8192, Partial: true, At: first}
	if len(got) != 2 || got[e.ids["shop"]] != wantShop || got[e.ids["notes"]] != wantNotes {
		t.Fatalf("Disk() = %+v, want shop %+v and notes %+v (blog is missing)", got, wantShop, wantNotes)
	}
	mu.Lock()
	if !slices.Equal(walked, []string{"shop", "notes"}) || peak != 1 {
		t.Fatalf("walked %v with %d at once, want shop then notes one at a time", walked, peak)
	}
	walked = nil
	failWalk["shop"] = true
	sizes["notes"] = 16384
	mu.Unlock()
	if n := len(e.cmd.callsOf(runner.DockerDF)); n != 1 {
		t.Fatalf("%d docker.df reads, want 1", n)
	}

	// The second run, an hour later: docker.df fails and so does shop's walk.
	e.clock.add(time.Hour)
	e.cmd.set(runner.DockerDF, "", errors.New("exit status 1"))
	if d := fa.fire(t, 1); d != time.Hour {
		t.Fatalf("next disk usage read after %s, want 1h", d)
	}
	waitFor(t, func() bool { return fa.count() >= 3 })
	got = m.Disk()
	wantNotes = DiskUsage{Volumes: 0, Folder: 16384, Partial: true, At: e.clock.now()}
	if got[e.ids["shop"]] != wantShop || got[e.ids["notes"]] != wantNotes {
		t.Fatalf("Disk() after failures = %+v, want shop kept %+v and notes %+v", got, wantShop, wantNotes)
	}
	if n := len(e.cmd.callsOf(runner.DockerDF)); n != 2 {
		t.Fatalf("%d docker.df reads, want 2", n)
	}

	// Close stops the loop while it waits.
	m.Close()
	if n := fa.count(); n != 3 {
		t.Fatalf("%d waits, want 3", n)
	}
}

// Without any good docker.df read the volumes are unknown, so the value is
// only a lower bound.
func TestDiskUsageWithoutVolumesIsPartial(t *testing.T) {
	e := newSamplerEnv(t)
	m := New(Options{Store: e.st, Cmd: e.cmd, Source: e.src, Host: Host{Proc: e.proc}, Now: e.clock.now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	e.cmd.set(runner.DockerDF, "", errors.New("exit status 1"))
	m.walk = func(context.Context, string) (int64, bool, error) { return 100, false, nil }
	m.readDisk(context.Background())
	if u := m.Disk()[e.ids["shop"]]; u != (DiskUsage{Folder: 100, Partial: true, At: e.clock.now()}) {
		t.Fatalf("Disk()[shop] = %+v, want folder 100, partial", u)
	}
}
