package main

import (
	"bytes"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestSQLInput(t *testing.T) {
	cases := []struct {
		input      string
		statements []string
		rest       string
		open       bool
	}{
		{"select 1;select 2;", []string{"select 1", "select 2"}, "", false},
		{"select 'a;it''s', \"a;\"\"b\";\nselect", []string{"select 'a;it''s', \"a;\"\"b\""}, "\nselect", false},
		{"-- ;\nselect /* ; /* nested */ */ 1;-- tail", []string{"-- ;\nselect /* ; /* nested */ */ 1"}, "-- tail", false},
		{"select 'open;", nil, "select 'open;", true},
		{"/* open;", nil, "/* open;", true},
	}
	for _, c := range cases {
		statements, rest, open := splitSQL(c.input)
		if !reflect.DeepEqual(statements, c.statements) || rest != c.rest || open != c.open {
			t.Errorf("splitSQL(%q) = %#v, %q, %v", c.input, statements, rest, open)
		}
	}
	for _, input := range []string{"", " ;\n", "-- trailing", "/* outer /* nested */ */ -- tail"} {
		if sqlBody(input) != "" {
			t.Errorf("comments treated as SQL: %q", input)
		}
	}
	for _, input := range []string{"-- comment\nselect 1", "/* comment */ select ';'"} {
		if sqlBody(input) == "" {
			t.Errorf("SQL lost: %q", input)
		}
	}
	if sqlBody("/* transaction */ -- begin\nBEGIN") != "BEGIN" {
		t.Fatal("transaction command hidden by leading comments")
	}
}

func TestShellInput(t *testing.T) {
	var out bytes.Buffer
	s := shell{out: &out, errOut: &out}
	// Commands inside a multiline string must stay SQL, not become CLI commands.
	err := s.read(strings.NewReader("select 'open\n\\q\n"), false)
	if err == nil || s.quit {
		t.Fatalf("unterminated string: err=%v, quit=%v", err, s.quit)
	}
	s = shell{out: &out, errOut: &out}
	if err := s.read(strings.NewReader("-- comment\n\\x\n\\timing\n\\p\n\\r\n\\q\nselect invalid;"), false); err != nil {
		t.Fatal(err)
	}
	if !s.quit || !s.expanded || !s.timing {
		t.Fatal("meta command state not updated")
	}
	s = shell{out: &out, errOut: &out}
	if err := s.read(strings.NewReader("\\unknown\n"), false); err == nil {
		t.Fatal("unknown command accepted")
	}
	s.includeDepth = 16
	if err := s.file("anything"); err == nil {
		t.Fatal("include recursion not bounded")
	}
	if identifier("\"") != "\"" || identifier("foo") != "FOO" || identifier(`"a""b"`) != `a"b` {
		t.Fatal("identifier quoting")
	}
	if literal("a'b") != "'a''b'" {
		t.Fatal("literal escaping")
	}
}

type testRows struct {
	sets     [][][]driver.Value
	set, row int
	failure  error
}

func (r *testRows) Columns() []string { return []string{"NAME", "VALUE"} }
func (r *testRows) Close() error      { return nil }
func (r *testRows) Next(dest []driver.Value) error {
	if r.failure != nil {
		return r.failure
	}
	if r.row == len(r.sets[r.set]) {
		return io.EOF
	}
	copy(dest, r.sets[r.set][r.row])
	r.row++
	return nil
}
func (r *testRows) HasNextResultSet() bool { return r.set+1 < len(r.sets) }
func (r *testRows) NextResultSet() error   { r.set++; r.row = 0; return nil }

func TestResultsAndCLI(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		var out bytes.Buffer
		s := shell{out: &out, expanded: expanded}
		rows := &testRows{sets: [][][]driver.Value{{{"中文", nil}, {[]byte("a\tb\nc"), int64(2)}}, {}}}
		if err := s.printRows(rows); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"NAME", "中文", "NULL", `a\tb\nc`, "(2 rows)", "(0 rows)"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q in %q", want, out.String())
			}
		}
		sentinel := errors.New("fetch failed")
		if err := s.printRows(&testRows{failure: sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("fetch error lost: %v", err)
		}
	}
	var out bytes.Buffer
	if code := run([]string{"--help"}, os.Stdin, &out, &out); code != 0 || !strings.Contains(out.String(), "\\dt") {
		t.Fatal("help requires connection")
	}
	t.Setenv("DMDSN", "")
	for _, args := range [][]string{{"-p", "0"}, {"--timeout", "-1s"}, {"unexpected"}} {
		if code := run(args, os.Stdin, &out, &out); code != 2 {
			t.Errorf("%v: exit=%d", args, code)
		}
	}
	t.Setenv("DMDSN", "dm://user:secret@localhost:5236")
	out.Reset()
	if code := run([]string{"--unknown"}, os.Stdin, &out, &out); code != 2 || strings.Contains(out.String(), "secret") {
		t.Fatal("invalid flags exposed DSN credentials")
	}
}
