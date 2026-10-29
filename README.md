# TextMapper

[![Build Status](https://github.com/inspirer/textmapper/actions/workflows/go.yml/badge.svg)](https://github.com/inspirer/textmapper/actions/workflows/go.yml)

## Introduction

TextMapper is a tool for language development. It generates bottom-up parsers with complete infrastructure from a high-level, declarative specification. TextMapper spreads the generative approach onto different aspects of language design and tries to generate as much derived, boilerplate code as possible. With a little effort you get an abstract syntax tree, code formatters, and even full-featured editor plug-ins for major IDEs.

TextMapper takes an annotated context-free grammar and outputs a program able to parse the language defined by the grammar. Generated parsers are deterministic and employ LALR(1) parser tables. In grammar handling aspects it is very similar to Bison, with some additional features on top. If you are familiar with Bison, you won't get lost. TextMapper includes quite a sophisticated scanner generator (Unicode-aware, specified using regular expressions, almost flex/JFlex compatible).

## Generalized parsing (Go)

By default TextMapper requires grammars to be deterministic LALR(1): unresolved shift/reduce and reduce/reduce conflicts fail the compilation. When generating Go code, a grammar can opt into the generalized parsing mode instead:

```
language mylang(go);

package = "example.com/mylang"
generalized = true
```

In this mode ordinary (finite) context-free grammars are accepted as written. Direct and indirect left recursion, epsilon productions, sequences, choices, and unresolved shift/reduce and reduce/reduce conflicts are all supported; the grammar no longer has to be rewritten into a deterministic one. `%expect` does not silence a conflict into a single chosen branch: every allowed branch is explored.

### Result of a Parse call

The generated API is the same as in the deterministic mode: a `Lexer` with the usual `Init`/`Next` semantics, a `Parser` with `Init()`, and one `Parse` (or `ParseName`) entry per `%input`, returning `(T, error)` for a typed start nonterminal and just `error` for an untyped one. All token values and byte offsets are preserved.

The decision is always made against the **whole** input, counting complete derivations of the start nonterminal followed by EOI:

* **No** complete derivation: a plain `SyntaxError` is returned. Extra tokens at the end of an otherwise valid prefix are not ignored, and a prefix with several candidates is not reported as ambiguous while later tokens still disambiguate it.
* **Exactly one** complete derivation: it is accepted and its typed semantic value is returned.
* **Two or more** structurally different complete derivations: an `*AmbiguousError` is returned, even when all derivations evaluate to the same semantic value. Two derivations are considered the same only when they use the same productions and the same subtree derivations everywhere.

Ambiguity is detected with the exported sentinel error:

```go
var p mylang.Parser
var l mylang.Lexer
l.Init(source)
_, err := p.Parse(&l)
if errors.Is(err, mylang.ErrAmbiguous) {
    // the input is ambiguous
}
```

`ErrAmbiguous` is a non-nil `error` value; `*AmbiguousError` wraps it (its `Unwrap` returns `ErrAmbiguous`), so `errors.Is` is the supported way of telling ambiguity apart from a syntax error.

### Semantic actions

Semantic actions never run during recognition. After the whole input is known to have exactly one complete derivation, the actions of that derivation are executed once each, in the original left-to-right reduction order, including mid-rule actions (which still see only the values and ranges of the already recognized prefix). `$$`, named and ordinal references (`$0` is the first RHS symbol in Go), token-associated values, and `offset`/`endoffset` all work as in deterministic parsers. An action returning an error stops the replay immediately; the error is propagated and actions that already ran are not undone. On syntax errors and ambiguities no grammar action runs at all, so failed or speculative branches cannot leave side effects. Grammar actions may log to the outside world, but must not read or modify parser state or influence recognition.

Lexer actions are different: they run while the input is tokenized (once per token, as usual) and are never rolled back, including on ambiguous or rejected input.

Operator precedence and associativity declarations remain in force: `%left`/`%right`/`%prec` still disambiguate conflicts into a single branch, and an expression forbidden by `%nonassoc` stays a syntax error; it is not re-accepted by a generalized branch.

Parsers keep no per-call state on the `Parser` value (all bookkeeping is local to the `Parse` call), so failed, ambiguous, or action-error parses do not affect subsequent calls on the same or on another parser instance.

### Unsupported combinations

The generalized engine targets ordinary typed (or untyped) grammars. The following features are explicitly rejected at compile time together with `generalized = true`: non-Go targets, `eventBased`/`eventFields`/`eventAST`, `tokenStream`, `genSelector`, `fixWhitespace`, `cancellable`/`cancellableFetch`, runtime lookahead predicates (`(?=)`), `lalr(k)` with `k > 1`, state markers (`lr0`, `greedy`, recovery scopes) and error recovery, custom `%%` templates, and nonterminals with a zero-length recursive cycle (which would allow infinitely many parses).

