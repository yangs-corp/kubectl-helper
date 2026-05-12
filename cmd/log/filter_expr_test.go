package main

import "testing"

func TestCompileLogMatcherKeepsPlainSubstringSearch(t *testing.T) {
	matcher, err := compileLogMatcher("connection refused")
	if err != nil {
		t.Fatalf("compileLogMatcher returned error: %v", err)
	}
	if !matcher("db connection refused after retry") {
		t.Fatal("expected plain multi-word query to behave as substring search")
	}
	if matcher("connection reset refused") {
		t.Fatal("expected plain multi-word query to require the exact substring")
	}
}

func TestCompileLogMatcherBooleanExpression(t *testing.T) {
	matcher, err := compileLogMatcher(`(error OR warn) AND "payment api"`)
	if err != nil {
		t.Fatalf("compileLogMatcher returned error: %v", err)
	}

	cases := []struct {
		line string
		want bool
	}{
		{line: "error from payment api", want: true},
		{line: "warn from payment api", want: true},
		{line: "error from billing api", want: false},
		{line: "info from payment api", want: false},
	}
	for _, tc := range cases {
		if got := matcher(tc.line); got != tc.want {
			t.Fatalf("matcher(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestCompileLogMatcherAndPrecedence(t *testing.T) {
	matcher, err := compileLogMatcher("error OR warn AND db")
	if err != nil {
		t.Fatalf("compileLogMatcher returned error: %v", err)
	}

	cases := []struct {
		line string
		want bool
	}{
		{line: "error from api", want: true},
		{line: "warn from db", want: true},
		{line: "warn from api", want: false},
	}
	for _, tc := range cases {
		if got := matcher(tc.line); got != tc.want {
			t.Fatalf("matcher(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestCompileLogMatcherRejectsInvalidExpression(t *testing.T) {
	if _, err := compileLogMatcher("error AND"); err == nil {
		t.Fatal("expected invalid expression to return an error")
	}
}
