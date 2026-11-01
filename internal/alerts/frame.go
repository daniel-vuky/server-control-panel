package alerts

import (
	"strconv"
	"time"
)

// Frame is what every message is framed with: the server's name, the time in
// the server zone and the link to the panel.
type Frame struct {
	Host      func() string // the hostname (docker.info when known, else os.Hostname); nil is none
	Zone      *time.Location
	PublicURL string // PANEL_PUBLIC_URL, no trailing slash; "" sends no link
	Now       func() time.Time
}

func (f Frame) now() time.Time {
	if f.Now == nil {
		return time.Now()
	}
	return f.Now()
}

func (f Frame) zone() *time.Location {
	if f.Zone == nil {
		return time.UTC
	}
	return f.Zone
}

// Footer is the host and the time now in the server zone:
// "prod-01 · 10:31 (Asia/Ho_Chi_Minh)".
func (f Frame) Footer() string { return f.footerAt(f.now()) }

func (f Frame) footerAt(t time.Time) string {
	z := f.zone()
	when := t.In(z).Format("15:04") + " (" + z.String() + ")"
	if f.Host == nil {
		return when
	}
	if h := f.Host(); h != "" {
		return h + " · " + when
	}
	return when
}

// link is the panel page of an alert: the project's detail page, else path
// ("/server", "/backups"); "" without a public URL or a page.
func (f Frame) link(projectID int64, path string) string {
	switch {
	case f.PublicURL == "":
		return ""
	case projectID != 0:
		return f.PublicURL + "/projects/" + strconv.FormatInt(projectID, 10)
	case path == "":
		return ""
	}
	return f.PublicURL + path
}
