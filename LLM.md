# hanzoai/semantic

Knowledge graphs, context graphs and decision intelligence for agents, in Go.
A native port of `semantica` (Python, `semantica-agi/semantica`, MIT) — same
model, no Python runtime, no torch.

Standard library only. `go.mod` has no `require` block and is not to gain one:
where the Python reaches for a library, the Go states an interface and reports
what is missing.

## Shape

    ingest → parse → normalize → split → extract → assert
                                              ↓
                              kg      the graph and its measures
                              ontology  the type system over it
                              reason  what else follows
                              dedupe  what is the same, what disagrees
                              agent   decisions, memory, causes
                              store   vector · graph · triple
                              prov    W3C PROV-O
                              export  the formats other tools read

`semantic.go` holds the values every stage passes — `Doc`, `Chunk`, `Triple` —
and one interface per stage. `Pipeline` composes them; every stage but the
first may be nil. `pipeline_test.go` runs the real packages end to end over a
temporary file and asserts the N-Triples that come out.

## Packages

| package | what it is | parity |
|---|---|---|
| `ingest` | files, directories, globs, HTTP, stdin; one `Origin` per document, hashed | full, minus the binary decoders |
| `parse` | text, markdown, HTML, JSON, CSV, XML, email; sections and detection | text formats full; binary formats report `ErrFormat` |
| `normalize` | NFC/NFD/NFKC/NFKD from an embedded UCD, encoding detection, mojibake repair, HTML stripping, running-head removal | full, and correct against the UAX #15 vectors |
| `split` | characters, words, tokens, sentences, paragraphs, recursive, markdown, code; a `Semantic` splitter over an embedding seam | full |
| `extract` | rules over a gazetteer and patterns, LLM behind a `Model` interface, coreference, schema checking | deterministic paths full; model paths are the interface |
| `kg` | the graph: folding, merging, centrality, communities, paths; `Mem` serves `store.Graph` and `store.Triple` | full |
| `ontology` | classes, properties, domains, ranges, hierarchy; a Turtle/N-Triples parser, JSON-LD, and schema inference from observed data | full, minus OWL restrictions |
| `dedupe` | blocking, similarity, clustering, merge, and the conflict rules that settle a disagreement | full |
| `reason` | Datalog over triples, semi-naive to a fixpoint, with the derivation kept | entailment full; SPARQL and abduction absent |
| `agent` | decisions, policies, precedent, memory, causal chains | full on the in-memory graph |
| `store` | three interfaces because they answer three questions — what is *like* this, what is *connected* to this, what was *asserted*. `Mem`, `File`, `qdrant`, `sparql` | in-memory and the two remote drivers |
| `export` | json, ndjson, jsonld, csv, tsv, nt, ttl, rdfxml, graphml, gexf, dot, mermaid, cypher | full for those; five of them read back |
| `decision`, `prov` | the record types the layers above write | declarations only |

## What is not here

Grouped by why, because the reasons differ and only one of them is work left
to do.

**Needs a model.** Every extraction path that runs a neural network: spaCy NER
and dependency relations, HuggingFace pipelines, sentence embeddings, Node2Vec,
docling, OCR. Each sits behind an interface — `extract.Model`, `split.Embed`,
`store.Vector` — so an adapter is a package, not a rewrite. The deterministic
path is complete and is what the tests exercise.

**Needs a library outside the standard one.** PDF, docx, xlsx, pptx, YAML,
Parquet, Arrow, and an approximate nearest-neighbour index. `parse` reports
`ErrFormat` and `export` reports `ErrLibrary`, each naming the format, so a
caller can tell "this build cannot" from "nobody has heard of it". The graph
drivers for Neo4j, FalkorDB, Neptune, AGE, Weaviate, Milvus, pgvector,
Pinecone, FAISS, sqlite-vec, Anzo and Oxigraph are registered by name and
return `ErrDriver`.

**Collapsed, because Go's types already say it.** Python dispatches on method
name strings through mutable registries — `get_split_method`,
`get_entity_method`, `get_deduplication_method`, and a dozen more. Here the
choice is a typed value checked at compile time. The functional `methods.py`
wrapper layer over each package's classes is likewise gone: the methods are the
API. Rete is not ported because the semi-naive fixpoint returns the same model;
hierarchical clustering is not ported because blocking already bounds the
comparison; backward chaining is not ported because `Run` to a fixpoint plus
`Model.Match` answers the same goals.

**Belongs to another package.** Serialization asked for inside `agent`, entity
linking asked for inside `agent` (`kg.Merge` does it), SKOS asked for inside
`agent` (`ontology` owns it), OWL export asked for inside `export` (it
serializes an ontology, not a stream of assertions), decision-shaped vector
paths asked for inside `store`. One home each.

**Presentation.** Investigation guides, recommendation prose, natural-language
explanations, progress trackers, per-package loggers. `reason.Model.Why`
returns the derivation as data and `dedupe.Conflict` carries every field a
guide would render; baking the English into a library fixes the wording for
every caller.

**Genuinely open.**

- `store.Vector` and `store.Triple` have no delete, so an erasure coordinator
  cannot fan out across stores. `agent` leaves a tombstone on the graph side
  only. Adding it is an interface change in `store/store.go`.
- `store.Triple`'s terms are `[3]string`, so an RDF literal's datatype and
  language tag have nowhere to go. Objects are written as plain literals.
- `ParseTurtle` resolves prefixed names to full IRIs and does not keep the
  bindings beyond the non-standard ones; `Schema.Turtle` writes every term in
  full, so no term identity is lost, only the header.
- `ontology.Namespace.Root` appends `/v<version>/` to a base that may already
  end in `#`, so a schema parsed from a versioned document mints unseen terms
  into a namespace its own terms do not use. Terms that were observed keep
  their IRIs; only newly minted ones move.
- `agent.Journal.Write` writes the decision node and its evidence edges under
  separate locks, so a concurrent reader can see a decision a moment before its
  evidence. `kg.Graph` has no transaction boundary.
- `reason.Model.Prov` is the only cross-package coupling in `reason`. Lifting
  it out is one method.

## Where the Go differs from the Python on purpose

Ported bugs are not parity. Each of these is covered by a test.

- `extract`'s reply repair closed a truncated JSON object by counting each
  bracket kind separately, so `{"a":[{...}` closed as `}]` — backwards, on the
  one input the function exists for. It now closes innermost-first off a stack
  and ignores brackets inside string literals. `providers.py:180` still has it.
- `extract`'s entity patterns joined words with `\s`, so a heading and the
  sentence beneath it read as one name and the real relation was lost. A name
  is now separated by horizontal space only. `methods.py:667` still has it.
- `extract`'s LLM replies decoded by plain struct matching, so `confidence`
  never reached `Score` and every provider's field aliases were dropped.
- `dedupe.Likeness` summed its weighted parts in map order. Float addition is
  not associative, so the same comparison scored differently between runs and
  `Find` returned different pairs. The parts are now weighed in sequence.
- `kg.Eigen` ran plain power iteration, which on a bipartite graph swings
  between the two ends of a ± eigenvalue pair forever: a star came back with
  every node scored 1. It iterates with A+I, which moves no eigenvector.
- `export`'s Cypher escaping escaped the backslash it had just inserted, so
  `it's` broke the statement. GEXF and DOT interpolated labels unescaped.
- `ontology.TermIRI` minted every unknown name as a class, so a property with
  no IRI of its own was written out Pascal-cased and `worksFor` came back as
  `Worksfor`.
- `ontology`'s inferred object properties named their endpoints by raw source
  type (`person`) while data properties named them by class (`Person`), so an
  inferred schema referred to classes it did not contain.
- `ontology.XSD` read a `time.Time` as `xsd:string`.
- `semantic.Pipeline.Run` called a method on a nil `Ingester`. The command
  built one and panicked on every invocation.
- `dedupe`'s severity, `reason`'s variable syntax, `store`'s Qdrant scores and
  `k <= 0`, and `export`'s predicate casing each differ from the Python where
  the Python loses information. The package doc comments say which and why.

## Conventions

Short names, no compound words — the package carries the meaning, so
`split.Recursive`, not `split.RecursiveTextSplitter`. `ctx` first. Accept
interfaces, return structs. Errors wrapped with `%w`. No panics. Doc comment on
every exported symbol, starting with its name, saying what the code does and
why.

## Build

    GOWORK=off go build ./... && GOWORK=off go vet ./... && GOWORK=off go test ./...

`GOWORK=off` is mandatory: a parent `go.work` otherwise hijacks the module.
Coverage runs 75% at the root to 99% in `reason`; `cmd/`, `decision/` and
`prov/` have no tests because they are a main package and two files of
declarations.
