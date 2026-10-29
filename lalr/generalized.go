package lalr

import "sort"

// populateGenActions builds Tables.GenActions: a per-state list of all parser
// actions that are legal on each lookahead terminal. Unresolved conflicts are
// preserved as multiple actions, letting a generalized (GLR-style) parser
// explore every alternative. Resolutions explicitly declared in the grammar
// (operator precedence, %left/%right/%nonassoc) are applied exactly as in the
// deterministic tables, so they never produce extra branches.
func (c *compiler) populateGenActions() {
	out := make([][]GenCell, len(c.states))
	for _, st := range c.states {
		var cells []GenCell

		if st.lr0 {
			if len(st.reduce) > 0 {
				// A default reduction is performed on every lookahead terminal.
				cells = append(cells, GenCell{
					Terminal: -1,
					Actions:  []int32{int32(st.reduce[0])},
				})
			}
			// Pure shift states need no cells: the runtime falls back to the goto
			// table and shifts whenever there is a transition for the terminal.
			out[st.index] = cells
			continue
		}

		type entry struct {
			shift bool
			rules []int32
		}
		perTerm := make(map[int32]*entry)
		get := func(term int32) *entry {
			e, ok := perTerm[term]
			if !ok {
				e = &entry{}
				perTerm[term] = e
			}
			return e
		}

		for _, target := range st.shifts {
			sym := int(c.states[target].symbol)
			if sym >= c.grammar.Terminals {
				break // terminal transitions come first (states are sorted by symbol)
			}
			get(int32(sym)).shift = true
		}

		if !c.useTransitions {
			for i, rule := range st.reduce {
				for _, term := range st.la[i] {
					get(int32(term)).rules = append(get(int32(term)).rules, int32(rule))
				}
			}
		}

		terms := make([]int32, 0, len(perTerm))
		for term := range perTerm {
			terms = append(terms, term)
		}
		sort.Slice(terms, func(i, j int) bool { return terms[i] < terms[j] })

		for _, term := range terms {
			e := perTerm[term]
			var actions []int32
			switch {
			case !e.shift:
				// One or more reductions. Multiple reductions form a reduce/reduce
				// conflict and all survive.
				actions = append(actions, e.rules...)
			case len(e.rules) == 1:
				// A shift/reduce conflict for a single rule. Precedence and
				// associativity declarations resolve it exactly as in the
				// deterministic tables.
				switch c.resolvePrec(int(e.rules[0]), Sym(term)) {
				case doShift:
					actions = append(actions, -1)
				case doReduce:
					actions = append(actions, e.rules[0])
				case doError:
					// %nonassoc: neither shifting nor reducing is allowed. The
					// empty cell is emitted on purpose so that the parser does not
					// fall back to the goto table and silently re-enables the
					// forbidden shift.
				default:
					actions = append(actions, -1, e.rules[0])
				}
			default:
				// A shift together with two or more competing reductions is an
				// unresolved conflict: all actions survive.
				actions = append(actions, -1)
				actions = append(actions, e.rules...)
			}
			// Always emit the cell, even when empty: an empty cell marks a terminal
			// that has been explicitly considered and rejected (%nonassoc).
			cells = append(cells, GenCell{Terminal: term, Actions: actions})
		}
		out[st.index] = cells
	}
	c.out.GenActions = out
}

// HasNullableCycle reports whether the grammar contains a nonterminal that can
// derive itself without consuming any input, e.g. "A: A | ;". Such grammars
// admit infinitely many derivations of (parts of) the input and cannot be
// parsed by the generalized parser, which therefore rejects them.
func HasNullableCycle(g *Grammar) bool {
	numTerm := g.Terminals
	numNT := len(g.Symbols) - numTerm
	if numNT <= 0 {
		return false
	}

	nullable := make([]bool, numNT)
	rulesByNT := make([][][]Sym, numNT)
	for _, r := range g.Rules {
		lhs := int(r.LHS) - numTerm
		if lhs < 0 || lhs >= numNT {
			continue
		}
		var rhs []Sym
		for _, s := range r.RHS {
			if s.IsStateMarker() {
				continue
			}
			rhs = append(rhs, s)
		}
		rulesByNT[lhs] = append(rulesByNT[lhs], rhs)
	}

	// Compute the least fixed point of "can derive an empty string".
	for {
		changed := false
		for nt, rules := range rulesByNT {
			if nullable[nt] {
				continue
			}
		rules:
			for _, rhs := range rules {
				for _, s := range rhs {
					if int(s) < numTerm || !nullable[int(s)-numTerm] {
						continue rules
					}
				}
				nullable[nt] = true
				changed = true
				break
			}
		}
		if !changed {
			break
		}
	}

	// Among nullable nonterminals, add an edge A->B for every rule A->alpha in
	// which every symbol of alpha is nullable and B occurs. A cycle (including a
	// self-loop) means A derives A without consuming input.
	edges := make([][]int, numNT)
	for nt, rules := range rulesByNT {
		if !nullable[nt] {
			continue
		}
		seen := make(map[int]bool)
		for _, rhs := range rules {
			allNullable := true
			for _, s := range rhs {
				isNT := int(s) >= numTerm
				if !isNT || !nullable[int(s)-numTerm] {
					allNullable = false
					break
				}
			}
			if !allNullable {
				continue
			}
			for _, s := range rhs {
				b := int(s) - numTerm
				if !seen[b] {
					seen[b] = true
					edges[nt] = append(edges[nt], b)
				}
			}
		}
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]byte, numNT)
	var dfs func(int) bool
	dfs = func(nt int) bool {
		color[nt] = gray
		for _, b := range edges[nt] {
			switch color[b] {
			case gray:
				return true
			case white:
				if dfs(b) {
					return true
				}
			}
		}
		color[nt] = black
		return false
	}
	for nt := 0; nt < numNT; nt++ {
		if color[nt] == white && dfs(nt) {
			return true
		}
	}
	return false
}
