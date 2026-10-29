#!/usr/bin/env bash
# Tiny assertion helpers for the deploy script tests. Source it, call the
# assert_* functions, and end with `summary`. The EXIT trap also prints the
# summary and turns any failed assertion into a non-zero exit status, so a
# test file that forgets to call `summary` still fails when it should.

_LIB_PASS=0
_LIB_FAIL=0
_LIB_DONE=0

_lib_pass() {
  _LIB_PASS=$((_LIB_PASS + 1))
  printf 'ok   %s\n' "$1"
}

_lib_fail() {
  _LIB_FAIL=$((_LIB_FAIL + 1))
  printf 'FAIL %s\n' "$1"
}

# assert_eq <expected> <actual> <name>
assert_eq() {
  if [[ "$1" == "$2" ]]; then
    _lib_pass "$3"
  else
    _lib_fail "$3"
    printf '       expected: [%s]\n       actual:   [%s]\n' "$1" "$2"
  fi
}

# assert_fails <name> <cmd...>: passes when the command exits non-zero.
# Output is discarded and the command runs in a subshell, so an `exit` inside
# it cannot end the test.
assert_fails() {
  local name=$1
  shift
  if ("$@") >/dev/null 2>&1; then
    _lib_fail "$name (command succeeded, expected failure)"
  else
    _lib_pass "$name"
  fi
}

# assert_contains <haystack> <needle> <name>
assert_contains() {
  if [[ "$1" == *"$2"* ]]; then
    _lib_pass "$3"
  else
    _lib_fail "$3"
    printf '       missing:  [%s]\n       in:       [%s]\n' "$2" "$1"
  fi
}

# assert_file_mode <mode> <path>: octal mode as stat -c %a prints it (e.g. 600).
assert_file_mode() {
  local actual
  actual=$(stat -c %a "$2" 2>/dev/null) || actual=missing
  if [[ "$actual" == "$1" ]]; then
    _lib_pass "mode of $2 is $1"
  else
    _lib_fail "mode of $2 is $1"
    printf '       actual:   [%s]\n' "$actual"
  fi
}

# summary: print the totals and exit non-zero if anything failed.
summary() {
  exit 0
}

# A test file may define test_cleanup; it runs once at exit, before the totals.
_lib_finish() {
  local rc=$?
  if declare -F test_cleanup >/dev/null; then
    test_cleanup
  fi
  if [[ $_LIB_DONE -eq 0 ]]; then
    _LIB_DONE=1
    printf '%s passed, %s failed\n' "$_LIB_PASS" "$_LIB_FAIL"
    if [[ $_LIB_FAIL -gt 0 ]]; then
      rc=1
    fi
  fi
  exit "$rc"
}
trap _lib_finish EXIT
