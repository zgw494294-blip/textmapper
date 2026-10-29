package gen_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inspirer/textmapper/compiler"
	"github.com/inspirer/textmapper/gen"
)

// generalizedGrammars are full .tm grammars compiled with generalized = true.
// Each case ships a Go test file that gets compiled and executed against the
// generated, standalone package.
var generalizedGrammars = []struct {
	name     string
	grammar  string
	testFile string
}{
	{
		name: "glr",
		grammar: `language glr(go);

package = "example.com/glr"
generalized = true

:: lexer

space: /[ \t\r\n]+/ (space)

NUMBER {int}: /[0-9]+/ { $$ = parseInt(l.Text()); lexNote("num") }
'+': /\+/
'*': /\*/
'-': /-/
'(': /\(/
')': /\)/

:: parser

%input Expr;

Expr {int} :
      Expr '+' Expr { $$ = plus($0, $2) }
    | Expr '*' Expr { $$ = mul($0, $2) }
    | '-' Expr      { $$ = neg($1) }
    | '(' Expr ')'  { $$ = $1; note("group") }
    | NUMBER        { $$ = $0 }
;
`,
		testFile: `package glr

import (
	"errors"
	"strconv"
	"testing"
)

var grammarLog []string
var lexerLog []string

func parseInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

func lexNote(s string) { lexerLog = append(lexerLog, s) }
func note(s string)    { grammarLog = append(grammarLog, s) }
func plus(a, b int) int {
	grammarLog = append(grammarLog, "+")
	return a + b
}
func mul(a, b int) int {
	grammarLog = append(grammarLog, "*")
	return a * b
}
func neg(a int) int {
	grammarLog = append(grammarLog, "u-")
	return -a
}

func parse(src string) (int, error) {
	var l Lexer
	l.Init(src)
	var p Parser
	p.Init()
	return p.Parse(&l)
}

func TestUnique(t *testing.T) {
	grammarLog = nil
	lexerLog = nil
	v, err := parse("1+2")
	if err != nil {
		t.Fatalf("1+2: %v", err)
	}
	if v != 3 {
		t.Errorf("1+2 = %v, want 3", v)
	}
	if got := strings2(grammarLog); got != "[+]" {
		t.Errorf("grammar actions = %v, want [+]", got)
	}
	if len(lexerLog) != 2 {
		t.Errorf("lexer actions = %v, want 2 nums", lexerLog)
	}
}

func strings2(s []string) string {
	out := "["
	for i, v := range s {
		if i > 0 {
			out += " "
		}
		out += v
	}
	return out + "]"
}

func TestAmbiguous(t *testing.T) {
	for _, src := range []string{"1+2+3", "1+2*3", "1*2+3"} {
		grammarLog = nil
		lexerLog = nil
		_, err := parse(src)
		if !errors.Is(err, ErrAmbiguous) {
			t.Errorf("%s: err = %v, want ErrAmbiguous", src, err)
		}
		if len(grammarLog) != 0 {
			t.Errorf("%s: grammar actions ran on ambiguity: %v", src, grammarLog)
		}
		if len(lexerLog) == 0 {
			t.Errorf("%s: lexer actions did not run", src)
		}
	}
}

func TestAmbiguousSameValue(t *testing.T) {
	// Both derivations of 1+2+3 evaluate to 6; they are still distinct parses.
	grammarLog = nil
	_, err := parse("1+2+3")
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
}

func TestPrefixResolves(t *testing.T) {
	grammarLog = nil
	v, err := parse("(1+2)*3")
	if err != nil {
		t.Fatalf("(1+2)*3: %v", err)
	}
	if v != 9 {
		t.Errorf("(1+2)*3 = %v, want 9", v)
	}
	if got := strings2(grammarLog); got != "[+ group *]" {
		t.Errorf("actions = %v, want [+ group *]", got)
	}
}

func TestLeftRecursionAndUnary(t *testing.T) {
	if v, err := parse("1*2"); err != nil || v != 2 {
		t.Errorf("1*2 = %v, %v", v, err)
	}
	if v, err := parse("-1"); err != nil || v != -1 {
		t.Errorf("-1 = %v, %v", v, err)
	}
	if v, err := parse("--1"); err != nil || v != 1 {
		t.Errorf("--1 = %v, %v", v, err)
	}
}

func TestSyntaxError(t *testing.T) {
	for _, src := range []string{"1+", "(1", "1 2", "+1", ""} {
		grammarLog = nil
		_, err := parse(src)
		if errors.Is(err, ErrAmbiguous) {
			t.Errorf("%q: got ErrAmbiguous, want syntax error", src)
		}
		if err == nil {
			t.Errorf("%q: got no error", src)
		}
		var se SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("%q: %T is not SyntaxError", src, err)
		}
		if len(grammarLog) != 0 {
			t.Errorf("%q: grammar actions ran on failure: %v", src, grammarLog)
		}
	}
}

func TestTrailingTokens(t *testing.T) {
	// A unique prefix parse must not hide trailing tokens.
	_, err := parse("1+2 3")
	if err == nil || errors.Is(err, ErrAmbiguous) {
		t.Errorf("1+2 3: err = %v, want syntax error", err)
	}
}

func TestReuseAfterFailure(t *testing.T) {
	_, _ = parse("1+2+3") // ambiguous
	_, _ = parse("1+")    // syntax error
	grammarLog = nil
	v, err := parse("1+2")
	if err != nil || v != 3 {
		t.Errorf("reuse: %v, %v", v, err)
	}

	var p Parser
	var l1 Lexer
	l1.Init("1+2+3")
	_, _ = p.Parse(&l1)
	var l2 Lexer
	l2.Init("1+2")
	v, err = p.Parse(&l2)
	if err != nil || v != 3 {
		t.Errorf("same parser reuse: %v, %v", v, err)
	}

	// A fresh parser instance must not observe any state from the failed one.
	var p2 Parser
	var l3 Lexer
	l3.Init("1+2")
	v, err = p2.Parse(&l3)
	if err != nil || v != 3 {
		t.Errorf("fresh parser: %v, %v", v, err)
	}
}
`,
	},
	{
		name: "glrprec",
		grammar: `language glrprec(go);

package = "example.com/glrprec"
generalized = true

:: lexer

space: /[ \t\r\n]+/ (space)

'+': /\+/
'=': /=/
'a': /a/

:: parser

%input E;

%left '+';
%nonassoc '=';

E {string} :
      E '+' E { $$ = "L(" + $0 + "+" + $2 + ")" }
    | E '=' E { $$ = "(" + $0 + "=" + $2 + ")" }
    | 'a'     { $$ = "a" }
;
`,
		testFile: `package glrprec

import (
	"errors"
	"testing"
)

func parse(src string) (string, error) {
	var l Lexer
	l.Init(src)
	var p Parser
	return p.Parse(&l)
}

func TestLeftAssoc(t *testing.T) {
	v, err := parse("a+a+a")
	if err != nil {
		t.Fatalf("a+a+a: %v", err)
	}
	if want := "L(L(a+a)+a)"; v != want {
		t.Errorf("a+a+a = %q, want %q", v, want)
	}
}

func TestNonassoc(t *testing.T) {
	if v, err := parse("a=a"); err != nil {
		t.Errorf("a=a: %v", err)
	} else if want := "(a=a)"; v != want {
		t.Errorf("a=a = %q, want %q", v, want)
	}
	_, err := parse("a=a=a")
	if err == nil {
		t.Errorf("a=a=a accepted")
	}
	if errors.Is(err, ErrAmbiguous) {
		t.Errorf("a=a=a: %v, nonassoc must produce a syntax error, not ambiguity", err)
	}
}

func TestMixedPrecUnique(t *testing.T) {
	if v, err := parse("a+a=a"); err != nil {
		t.Errorf("a+a=a: %v", err)
	} else if want := "L(a+(a=a))"; v != want {
		t.Errorf("a+a=a = %q, want %q", v, want)
	}
	if v, err := parse("a=a+a"); err != nil {
		t.Errorf("a=a+a: %v", err)
	} else if want := "L((a=a)+a)"; v != want {
		t.Errorf("a=a+a = %q, want %q", v, want)
	}
}
`,
	},
	{
		name: "glrrr",
		grammar: `language glrrr(go);

package = "example.com/glrrr"
generalized = true

:: lexer

space: /[ \t\r\n]+/ (space)

'x': /x/
'y': /y/
'a': /a/
'b': /b/
'!': /!/
'?': /\?/

:: parser

%input One, Two, RR;

One :
      'x' Sub '!'
    | 'x' Sub '?'
;

Sub : 'y' ;

Two :
      'a'? 'b'
;

RR :
      'x' A '!'
    | 'x' B '!'
;

A : 'y' ;
B : 'y' ;
`,
		testFile: `package glrrr

import (
	"errors"
	"testing"
)

func TestPrefixBranchesResolve(t *testing.T) {
	var p Parser

	var l1 Lexer
	l1.Init("xy!")
	if err := p.ParseOne(&l1); err != nil {
		t.Errorf("xy!: %v", err)
	}
	var l2 Lexer
	l2.Init("xy?")
	if err := p.ParseOne(&l2); err != nil {
		t.Errorf("xy?: %v", err)
	}
	var l3 Lexer
	l3.Init("xy.")
	if err := p.ParseOne(&l3); err == nil || errors.Is(err, ErrAmbiguous) {
		t.Errorf("xy.: %v, want syntax error", err)
	}
}

func TestEpsilonChoices(t *testing.T) {
	var p Parser
	var l1 Lexer
	l1.Init("b")
	if err := p.ParseTwo(&l1); err != nil {
		t.Errorf("b: %v", err)
	}
	var l2 Lexer
	l2.Init("ab")
	if err := p.ParseTwo(&l2); err != nil {
		t.Errorf("ab: %v", err)
	}
	var l3 Lexer
	l3.Init("abb")
	if err := p.ParseTwo(&l3); err == nil {
		t.Errorf("abb accepted")
	}
}

func TestReduceReduceAmbiguity(t *testing.T) {
	var p Parser
	var l Lexer
	l.Init("xy!")
	err := p.ParseRr(&l)
	if !errors.Is(err, ErrAmbiguous) {
		t.Errorf("xy! RR: %v, want ErrAmbiguous", err)
	}
}
`,
	},
	{
		name: "glrir",
		grammar: `language glrir(go);

package = "example.com/glrir"
generalized = true

:: lexer

space: /[ \t\r\n]+/ (space)

'x': /x/
'y': /y/

:: parser

%input S;

S {int} : A { $$ = $0 } ;

A {int} :
      B 'x' { $$ = $0 + 10 }
    | 'x'   { $$ = 0 }
;

B {int} :
      A 'y' { $$ = $0 }
    | 'y'   { $$ = 0 }
;
`,
		testFile: `package glrir

import "testing"

func parse(src string) (int, error) {
	var l Lexer
	l.Init(src)
	var p Parser
	return p.Parse(&l)
}

func TestIndirectLeftRecursion(t *testing.T) {
	cases := []struct {
		src  string
		want int
	}{
		{"x", 0},
		{"yx", 10},
		{"xyx", 10},
		{"yxyx", 20},
	}
	for _, c := range cases {
		v, err := parse(c.src)
		if err != nil {
			t.Errorf("%s: %v", c.src, err)
			continue
		}
		if v != c.want {
			t.Errorf("%s = %v, want %v", c.src, v, c.want)
		}
	}
}

func TestRejects(t *testing.T) {
	if _, err := parse("y"); err == nil {
		t.Error("y accepted")
	}
	if _, err := parse("xx"); err == nil {
		t.Error("xx accepted")
	}
}
`,
	},
	{
		name: "glrerr",
		grammar: `language glrerr(go);

package = "example.com/glrerr"
generalized = true

:: lexer

space: /[ \t\r\n]+/ (space)

'a': /a/
'b': /b/

:: parser

%input S;

S {int} :
      'a' { note("mid") } 'b' { note("end"); $$ = 1; return errBoom }
;
`,
		testFile: `package glrerr

import (
	"errors"
	"testing"
)

var errBoom = errors.New("boom")
var log []string

func note(s string) { log = append(log, s) }

func TestActionError(t *testing.T) {
	log = nil
	var l Lexer
	l.Init("ab")
	var p Parser
	_, err := p.Parse(&l)
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if got := len(log); got != 2 || log[0] != "mid" || log[1] != "end" {
		t.Fatalf("log = %v, want [mid end]", log)
	}

	// The action error must not leave any parser state behind.
	var l2 Lexer
	l2.Init("ab")
	_, err = p.Parse(&l2)
	if !errors.Is(err, errBoom) {
		t.Fatalf("second parse err = %v, want boom", err)
	}
	if len(log) != 4 {
		t.Fatalf("log = %v, want 4 entries", log)
	}
}

func TestSyntaxErrorNoActions(t *testing.T) {
	log = nil
	var l Lexer
	l.Init("a")
	var p Parser
	_, err := p.Parse(&l)
	if err == nil {
		t.Fatal("a accepted")
	}
	if len(log) != 0 {
		t.Fatalf("log = %v, want no grammar actions", log)
	}
}
`,
	},
	{
		name: "glreps",
		grammar: `language glreps(go);

package = "example.com/glreps"
generalized = true
tokenLine = false

:: lexer

space: /[ \t\r\n]+/ (space)

'x': /x/

:: parser

%input S;

// %expect must not make the generalized parser pick a single conflict branch.
%expect 0;

S {string} : A B { $$ = $0 + "|" + $1 } ;

A {string} :
      'x' { $$ = "X" }
    |     { $$ = "e" }
;

B {string} :
      'x' { $$ = "X" }
    |     { $$ = "e" }
;
`,
		testFile: `package glreps

import (
	"errors"
	"testing"
)

func parse(src string) (string, error) {
	var l Lexer
	l.Init(src)
	var p Parser
	return p.Parse(&l)
}

func TestEpsilonUnique(t *testing.T) {
	v, err := parse("")
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if v != "e|e" {
		t.Errorf("empty = %q, want e|e", v)
	}
	if v, err := parse("xx"); err != nil {
		t.Errorf("xx: %v", err)
	} else if v != "X|X" {
		t.Errorf("xx = %q, want X|X", v)
	}
}

func TestEpsilonAmbiguity(t *testing.T) {
	// One x: either A consumes it and B is empty or vice versa.
	_, err := parse("x")
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("x: err = %v, want ErrAmbiguous", err)
	}
}

func TestEpsilonTrailing(t *testing.T) {
	if _, err := parse("xxx"); err == nil {
		t.Error("xxx accepted")
	}
}
`,
	},
	{
		name: "glrpos",
		grammar: `language glrpos(go);

package = "example.com/glrpos"
generalized = true

:: lexer

space: /[ \t\r\n]+/ (space)

'x' {int}: /x/ { $$ = 42 }

:: parser

%input S;

S {string} :
      'x'[a] { note($a, ${a.offset}, ${a.endoffset}) } 'x'[b] { note($b, ${b.offset}, ${b.endoffset}); $$ = "ok" }
;
`,
		testFile: `package glrpos

import (
	"strings"
	"testing"
)

var log []string

func note(v, s, e int) {
	var b strings.Builder
	b.WriteString("[")
	b.WriteRune('0' + rune(v))
	b.WriteRune(',')
	b.WriteRune('0' + rune(s))
	b.WriteRune(',')
	b.WriteRune('0' + rune(e))
	b.WriteString("]")
	log = append(log, b.String())
}

func TestPositions(t *testing.T) {
	log = nil
	var l Lexer
	l.Init(" x x ")
	var p Parser
	v, err := p.Parse(&l)
	if err != nil {
		t.Fatal(err)
	}
	if v != "ok" {
		t.Fatalf("value = %q", v)
	}
	if len(log) != 2 || log[0] != "[Z,1,2]" || log[1] != "[Z,3,4]" {
		t.Fatalf("log = %v", log)
	}
}
`,
	},
}

type genFileWriter struct{ dir string }

func (w *genFileWriter) Write(filename, content string) error {
	full := filepath.Join(w.dir, filepath.FromSlash(filename))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

func TestGeneralizedGrammars(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	for _, tc := range generalizedGrammars {
		t.Run(tc.name, func(t *testing.T) {
			g, err := compiler.Compile(context.Background(), tc.name+".tm", tc.grammar, compiler.Params{})
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if !g.Options.Generalized {
				t.Fatal("generalized option lost during compilation")
			}

			dir := filepath.Join(t.TempDir(), tc.name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			w := &genFileWriter{dir: dir}
			if err := gen.Generate(g, w, gen.Options{NoBuiltins: false}); err != nil {
				t.Fatalf("generate: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/"+tc.name+"\n\ngo 1.23\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "parser_extra_test.go"), []byte(tc.testFile), 0o644); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("go", "test", "-count=1", "./...")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOPROXY=off")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go test failed: %v\n%s", err, out)
			}
		})
	}
}

// TestGeneralizedRejections checks that unsupported feature combinations are
// rejected at compile time with a clear diagnostic.
func TestGeneralizedRejections(t *testing.T) {
	base := func(body string) string {
		return `language rej(go);

package = "example.com/rej"
generalized = true
` + body + `
:: lexer

space: /[ \t\r\n]+/ (space)
'x': /x/
'=': /=/

:: parser

%input S;

S : 'x' ;
`
	}
	cases := []struct {
		name    string
		grammar string
		want    string
	}{
		{"eventBased", base("eventBased = true\n"), "eventBased"},
		{"tokenStream", base("tokenStream = true\n"), "tokenStream"},
		{"cancellable", base("cancellable = true\n"), "cancellable"},
		{"lookaheadPredicate", `language rej(go);

package = "example.com/rej"
generalized = true

:: lexer

space: /[ \t\r\n]+/ (space)
'x': /x/
'a': /a/

:: parser

%input S;

S : (?= A) 'x' ;
A : 'a' ;
`, "lookahead predicates"},
		{"lalr2", `language rej(go);

package = "example.com/rej"
generalized = true

:: lexer

space: /[ \t\r\n]+/ (space)
'x': /x/

:: parser lalr(2)

%input S;

S : 'x' ;
`, "LALR(k)"},
		{"nullableCycle", `language rej(go);

package = "example.com/rej"
generalized = true

:: lexer

space: /[ \t\r\n]+/ (space)
'x': /x/

:: parser

%input S;

S : A 'x' ;
A : A | ;
`, "zero-length recursive"},
		{"tsTarget", `language rej(ts);

package = "example.com/rej"
generalized = true

:: lexer

'x': /x/

:: parser

%input S;

S : 'x' ;
`, "cannot be used when generating into ts"},
		{"customTemplate", `language rej(go);

package = "example.com/rej"
generalized = true

:: lexer

space: /[ \t\r\n]+/ (space)
'x': /x/

:: parser

%input S;

S : 'x' ;

%%
{{ define "parserVars" }}
	custom int
{{ end }}
`, "custom templates"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compiler.Compile(context.Background(), tc.name+".tm", tc.grammar, compiler.Params{})
			if err == nil {
				t.Fatal("compilation succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestDeterministicConflictsStillFail makes sure the deterministic mode keeps
// its original conflict policy: unresolved conflicts are compilation errors.
func TestDeterministicConflictsStillFail(t *testing.T) {
	const src = `language det(go);

package = "example.com/det"

:: lexer

space: /[ \t\r\n]+/ (space)
NUMBER: /[0-9]+/
'+': /\+/

:: parser

%input E;

E : E '+' E | NUMBER ;
`
	_, err := compiler.Compile(context.Background(), "det.tm", src, compiler.Params{})
	if err == nil {
		t.Fatal("conflicting deterministic grammar compiled without errors")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("err = %v, want a conflict diagnostic", err)
	}
}
