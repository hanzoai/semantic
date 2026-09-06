# hanzoai/semantic

Knowledge graphs, context graphs and decision intelligence for agents, in Go.
A native port of `semantica` (Python, `semantica-agi/semantica`, MIT) — same
model, no Python runtime, no torch.

## Shape

    ingest → parse → split → extract → assert
                                  ↓
                    decision (the layer the graph exists for)
                    prov     (W3C PROV-O: who did what, from what)
                    store    (vector · graph · triple)

- `semantic.go` — the values (`Doc`, `Chunk`, `Triple`) and one interface per
  stage. `Pipeline` is their composition; a nil stage is skipped.
- `store/` — three interfaces because they answer three questions: what is
  *like* this (Vector), what is *connected* to this (Graph), what was
  *asserted* (Triple). One engine may back all three.
- `decision/` — `Record`, `Policy`, `Recorder`, `Cause`. Named for the act,
  since the data is already the graph. Not `context/`: that shadows stdlib.
- `prov/` — PROV-O vocabulary kept verbatim so the record joins other PROV
  data without translation.

## Parity

The Python tests are the oracle. A package is done when its behaviour matches
`semantica/<pkg>`'s tests, not when it compiles.

Open design decision: extraction. Python leans on torch/spacy. Go needs either
ONNX runtime bindings or an embedding service behind the `Extractor` interface —
the interface is deliberately narrow so the choice stays reversible.

## Build

    GOWORK=off go build ./... && go vet ./...
