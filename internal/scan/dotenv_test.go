package scan

import (
	"reflect"
	"slices"
	"testing"
)

func TestParseDotenv(t *testing.T) {
	in := "export A=1\n" +
		"B=\"two words\"\n" +
		"C='x#y'\n" +
		"D=v # c\n" +
		"#E=1\n" +
		"\n" +
		"   \n" +
		"F=\n" +
		"G=a#b\n" +
		"H=\"q\" # tail\r\n" +
		"I=\"a\\\"b\"\n" +
		"PASSWORD=\"ab\\\"cd\" # note\n" +
		"J=\"back\\\\slash\"\n" +
		"K=\"ends\\\\\"\n" +
		"not a pair\n"
	want := map[string]string{
		"A":        "1",
		"B":        "two words",
		"C":        "x#y",
		"D":        "v",
		"F":        "",
		"G":        "a#b",
		"H":        "q",
		"I":        `a"b`,
		"PASSWORD": `ab"cd`,
		"J":        `back\slash`,
		"K":        `ends\`,
	}
	if got := ParseDotenv([]byte(in)); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseDotenv = %v, want %v", got, want)
	}
}

func TestSecretValuesRawUserinfo(t *testing.T) {
	env := map[string]string{ //nolint:gosec // fake credentials, the test subject
		"LINK":  "postgres://u:p%40ssword@h/d",
		"OTHER": "postgres://robert:pa/ssword@h/d",
		"NOTE":  "mysql://ab:cd@h/d",
	}
	got := SecretValues(env)
	for _, want := range []string{"p%40ssword", "p@ssword", "robert", "pa/ssword"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	for _, not := range []string{"u", "ab", "cd"} {
		if slices.Contains(got, not) {
			t.Errorf("unexpected %q in %v", not, got)
		}
	}
}

func TestSecretValues(t *testing.T) {
	env := map[string]string{ //nolint:gosec // fake credentials, the test subject
		"DB_PASSWORD":  "hunter22",
		"API_KEY":      "abcd",
		"PORT":         "8080",
		"X_TOKEN":      "abc",
		"DATABASE_URL": "postgres://shop:devpassword@db/shop",
		"db_secret":    "hunter22",
		"PLAIN_URLISH": "https://u:p@example.com/x",
		"NAME":         "postgres://robert:longpass@h/d",
	}
	got := SecretValues(env)
	for _, want := range []string{"hunter22", "abcd", "postgres://shop:devpassword@db/shop", "shop", "devpassword", "longpass", "robert"} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	for _, not := range []string{"8080", "abc", "u", "p"} {
		if slices.Contains(got, not) {
			t.Errorf("unexpected %q in %v", not, got)
		}
	}
	seen := map[string]bool{}
	for _, v := range got {
		if seen[v] {
			t.Errorf("duplicate %q", v)
		}
		seen[v] = true
	}
}
