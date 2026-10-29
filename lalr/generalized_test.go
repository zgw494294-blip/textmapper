package lalr

import "testing"

func TestHasNullableCycle(t *testing.T) {
	const (
		eoi Sym = 0
		x   Sym = 1 // terminal
		s   Sym = 2
		a   Sym = 3
		b   Sym = 4
	)
	names := map[Sym]string{eoi: "eoi", x: "x", s: "S", a: "A", b: "B"}
	name := func(g *Grammar) []string {
		ret := make([]string, len(names))
		for sym, n := range names {
			ret[int(sym)] = n
		}
		return ret
	}

	cases := []struct {
		desc  string
		rules []Rule
		want  bool
	}{
		{
			desc:  "direct epsilon self-cycle",
			rules: []Rule{{LHS: a, RHS: nil}, {LHS: a, RHS: []Sym{a}}},
			want:  true,
		},
		{
			desc: "indirect epsilon cycle",
			rules: []Rule{
				{LHS: s, RHS: []Sym{a, b}},
				{LHS: a},
				{LHS: b},
				{LHS: b, RHS: []Sym{s}},
			},
			want: true,
		},
		{
			desc: "left recursion consuming input",
			rules: []Rule{
				{LHS: a},
				{LHS: a, RHS: []Sym{a, x}},
			},
			want: false,
		},
		{
			desc: "plain nullable rules",
			rules: []Rule{
				{LHS: s, RHS: []Sym{a, b}},
				{LHS: a},
				{LHS: b, RHS: []Sym{x}},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			g := &Grammar{
				Terminals: 2,
				Symbols:   name(nil),
				Inputs:    []Input{{Nonterminal: s, Eoi: true}},
				Rules:     tc.rules,
			}
			if got := HasNullableCycle(g); got != tc.want {
				t.Errorf("HasNullableCycle() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGenActionsKeepConflicts(t *testing.T) {
	const (
		eoi    Sym = 0
		plus   Sym = 1
		number Sym = 2
		expr   Sym = 3
	)
	g := &Grammar{
		Terminals: 3,
		Symbols:   []string{"eoi", "+", "number", "Expr"},
		Inputs:    []Input{{Nonterminal: expr, Eoi: true}},
		Rules: []Rule{
			{LHS: expr, RHS: []Sym{expr, plus, expr}},
			{LHS: expr, RHS: []Sym{number}},
		},
	}
	tables, err := Compile(g, Options{AllowConflicts: true})
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}
	if len(tables.GenActions) != tables.NumStates {
		t.Fatalf("GenActions has %v states, want %v", len(tables.GenActions), tables.NumStates)
	}

	var foundConflict bool
	for _, cells := range tables.GenActions {
		for _, cell := range cells {
			hasShift, reduces := false, 0
			for _, a := range cell.Actions {
				if a == -1 {
					hasShift = true
				} else {
					reduces++
				}
			}
			if hasShift && reduces > 0 {
				foundConflict = true
			}
		}
	}
	if !foundConflict {
		t.Error("no shift/reduce conflict cell found in GenActions")
	}
}

func TestGenActionsNonassoc(t *testing.T) {
	const (
		eoi    Sym = 0
		eq     Sym = 1
		number Sym = 2
		expr   Sym = 3
	)
	g := &Grammar{
		Terminals: 3,
		Symbols:   []string{"eoi", "=", "number", "Expr"},
		Inputs:    []Input{{Nonterminal: expr, Eoi: true}},
		Precedence: []Precedence{
			{Associativity: NonAssoc, Terminals: []Sym{eq}},
		},
		Rules: []Rule{
			{LHS: expr, RHS: []Sym{expr, eq, expr}},
			{LHS: expr, RHS: []Sym{number}},
		},
	}
	tables, err := Compile(g, Options{AllowConflicts: true})
	if err != nil {
		t.Fatalf("Compile() error: %v", err)
	}

	var foundForbidden bool
	for _, cells := range tables.GenActions {
		for _, cell := range cells {
			if cell.Terminal == int32(eq) && len(cell.Actions) == 0 {
				foundForbidden = true
			}
		}
	}
	if !foundForbidden {
		t.Error("no explicit empty cell for the %nonassoc operator found")
	}
}
