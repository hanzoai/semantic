# Examples

Eighteen programs, one per capability. Each is a directory with a `main.go`
that says at the top what it demonstrates, and each runs on its own:

    GOWORK=off go run ./examples/pipeline

`GOWORK=off` is mandatory here as everywhere in this module: a parent `go.work`
otherwise hijacks it.

None of them reaches the internet or asks a model. Where a stage needs
something this module does not compute — an embedding, a completion, a running
Qdrant — the example supplies a deterministic stand-in behind the same
interface and says so in the file. The HTTP paths run against servers the
example starts on loopback, so what they exercise is the real request the real
driver sends.

Start with `pipeline`. It is the shortest honest answer to what this module
does, and every other example is one stage of it looked at closely.

| example | what it shows |
|---|---|
| `pipeline` | ingest → parse → normalize → split → extract → fold → export, end to end, printing what crosses each seam |
| `ingest` | files, JSON, JSON Lines, CSV, directory walks, globs, streams, HTTP, and the origin every document carries; the registry, and the refusal that guards a fetch of an internal address |
| `parse` | markdown, HTML, JSON, CSV, XML, email and docx: sections, links, tags and the decoded value; format detection; the seam for a format this module does not read |
| `normalize` | the four Unicode forms, encoding detection, mojibake repair, punctuation and whitespace, running heads, and a `Chain` assembled for one corpus |
| `split` | nine splitters on one document, with the tiling property checked; overlap, a real tokenizer behind `Count`, and `Or` falling back from a splitter that needs a model |
| `extract` | rules over patterns and a gazetteer, relations, proximity, the schema and confidence checks (which are orthogonal), endpoint binding, and the LLM path with its JSON recovery |
| `coref` | mentions, pronouns bound to the nearest admissible name, chains — and the relation count before and after, which is what the pass is for |
| `kg` | folding, four centrality measures that disagree on purpose, communities, components, bridges, paths, neighbourhoods, repetition, and a merge driven from outside |
| `dedupe` | likeness with its parts, five string metrics, SimHash and MinHash screens, blocking, groups, five merge strategies with the clash record, and incremental matching |
| `conflicts` | six kinds of disagreement, the tally a reviewer reads, and six rules for settling one — including the rule that settles nothing and says so |
| `ontology` | a schema inferred from observed data, naming conventions, hierarchy and cycles, Turtle and JSON-LD round trips, IRI minting, and the collision it refuses to paper over |
| `reason` | transitive, symmetric and inverse rules, a class hierarchy, Datalog rules parsed from text, the derivation of a fact, querying the closure, and the two ways a run stops short |
| `agent` | a credit desk: policies as data, a refusal recorded as a decision, causal chains, the story behind a choice, precedent search, and what a rule change would cost |
| `temporal` | spans on nodes and edges, the graph as it stood at four moments, retract against purge, the tombstone each leaves, time-aware traversal, and an audit trail kept outside the graph |
| `store` | vectors, edges and assertions on one object; filters and namespaces; three metrics; reciprocal-rank and weighted-sum fusion; and the same store durable in a file |
| `drivers` | every registered driver, the three different answers `Open` can give, a driver of your own, and the wire protocol the Qdrant and SPARQL drivers actually speak |
| `export` | thirteen formats written, five read back, format conversion in one call, the three kinds of source, and the four things the package refuses to do quietly |
| `prov` | a derived claim traced back through reasoning, extraction, splitting and ingestion to two files and the SHA-256 of each |

## Against the Python cookbook

The Python framework ships 40 notebooks: 26 in `introduction/`, 11 in
`advanced/`, 3 in `integrations/`, plus one `.py`. The introduction set defines
what the framework claims to do, so it is the parity oracle. What follows is
where each of the 26 landed, and what did not land.

| Python notebook | here |
|---|---|
| 01 Welcome to Semantica | `pipeline`, and this table |
| 02 Data Ingestion | `ingest` — partial, see below |
| 03 Document Parsing | `parse` — partial |
| 04 Data Normalization | `normalize` — partial |
| 05 Entity Extraction | `extract` — partial |
| 06 Relation Extraction | `extract` — partial |
| 07 Building Knowledge Graphs | `kg`, `dedupe` |
| 08 Your First Knowledge Graph | `pipeline` |
| 09 Graph Store | `store`, `drivers` — partial |
| 10 Graph Analytics | `kg` |
| 11 Chunking and Splitting | `split` — partial |
| 12 Embedding Generation | not ported |
| 13 Vector Store | `store` — partial |
| 14 Ontology | `ontology` — partial |
| 15 Export | `export` — partial |
| 16 Visualization | not ported |
| 17 Conflict Detection and Resolution | `conflicts` — partial |
| 18 Deduplication | `dedupe` |
| 19 Context Module | `agent` — entity linking lives in `kg` |
| 20 Triplet Store | `store`, `drivers` |
| 21 Amazon Neptune Store | not ported |
| 22 Provenance Tracking | `prov` — partial |
| 23 Reasoning | `reason` — partial |
| 24 Change Management | `temporal` — partial |
| 25 Seed Data | folded into `ingest` and `pipeline` |
| 26 Semantic Layer Basics | `ontology` — partial |

The `advanced/` set is the same capabilities at greater length and maps onto
the same examples: advanced extraction to `extract`, advanced graph analytics
to `kg`, multi-format export to `export`, multi-source integration to `ingest`
with `dedupe` and `conflicts`, reasoning and Datalog-style reasoning to
`reason`, temporal knowledge graphs to `temporal`, advanced context engineering
to `agent`, unstructured-to-ontology to `ontology`, advanced vector search to
`store`. Two do not map: the visualization suite, and the Snowflake ingestion
script.

## What did not port, and why

The reasons differ, and only some of them are work left to do.

### It needs a model

Nothing here runs a neural network, and no package in this module reaches the
network on its own. Each of these sits behind an interface — `extract.Model`,
`split.Embed`, `store.Vector` — so an adapter is a package rather than a
rewrite.

- **Embedding generation** (notebook 12) has no Go equivalent at all. The
  `store` and `split` examples use a bag of words over a fixed vocabulary,
  which is a real vector function and not a language model; it is enough to
  make the ranking and the semantic split reproducible offline, and it is not a
  substitute for a sentence transformer.
- **spaCy NER and dependency-parse relations** (notebooks 05, 06). The
  deterministic path — a gazetteer, entity shape patterns, verb-phrase relation
  patterns, proximity — is complete and is what `extract` shows.
- **The LLM extraction path** is present and exercised, but against a scripted
  model. What this module supplies around a real call is the prompt, the
  recovery of JSON from whatever the model wrapped it in, the field aliases
  every provider spells differently, and the binding of a named endpoint back
  to a known entity. All four run in the `extract` example.
- **LLM ontology generation and competency questions** (notebook 14).
  `ontology.Inference` reads a schema off observed data without a model, which
  is what the example shows.

### It needs a library outside the standard one

`go.mod` has no `require` block and is not to gain one.

- **PDF, and the legacy binary Office formats doc, xls and ppt** (notebook
  03). `parse` registers them as placeholders that report `ErrFormat`, so a
  caller can tell "this build cannot" from "nobody has heard of it". The
  `parse` example fills the gap with one `Register` call. docx, xlsx and pptx
  are zips of XML and are read with archive/zip and encoding/xml.
- **Parquet and YAML** (notebook 15). `export` registers them as `Absent` and
  reports `ErrLibrary`. The `export` example registers a writer under
  `parquet` and reaches it the same way as the rest.
- **An approximate nearest-neighbour index** (notebook 13). `store.Mem` ranks
  exactly, over every vector.

### It is a driver nobody has written yet

The names are registered so the set of stores this module knows about is one
list a program can read. `Open` on any of them returns `ErrDriver`, which the
`drivers` example shows beside a name that opens and a name nobody registered.

- **Graph stores**: Neo4j, FalkorDB, Apache AGE, **Amazon Neptune**
  (notebook 21).
- **Vector stores**: FAISS, Milvus, pgvector, Pinecone, Weaviate, sqlite.
- **Triple stores**: Anzo, Oxigraph.
- **Ingest sources** (notebook 02): databases, cloud object storage, email
  mailboxes, RSS and Atom feeds, message streams, git repositories, MCP
  servers, and Snowflake. There is also no robots.txt check and no sitemap
  crawler. Each is one `semantic.Ingester` registered under its scheme; the
  `ingest` example writes one in four lines to show the shape.

Qdrant and SPARQL are written, and the `drivers` example runs both against stub
servers on loopback so the requests they send are visible. One SPARQL driver
covers Fuseki, Blazegraph, RDF4J and GraphDB, because the protocol is the
interface.

### It is a notebook, not a library

- **Visualization** (notebook 16, advanced 03). There is no plotting here and
  no image is produced. The Go answer is to export the graph in a format a
  drawing tool reads — dot, mermaid, graphml, gexf, cypher — which the `export`
  example does, and to render it elsewhere.
- **Investigation guides and explanation prose** (notebooks 17, 23).
  `reason.Model.Why` returns the derivation as data and `dedupe.Conflict`
  carries every field a guide would render. Baking the English into a library
  fixes the wording for every caller, so the examples print it instead.

### The service is not one we run

- **The three Agno notebooks** in `integrations/`. Agno is a Python agent
  framework; there is no Go client for it, and porting the notebooks would mean
  writing one. What the notebooks demonstrate about this module — a shared
  context graph, decision records across agents, graph-backed retrieval — is
  what the `agent` example shows without the framework.

### Collapsed, because Go's types already say it

The Python dispatches on method-name strings through mutable registries and
wraps every class in a functional `methods.py`. Here the choice is a typed
value checked at compile time, and the methods are the API. The examples
therefore have no equivalent of `get_split_method` and its dozen siblings —
`split.New(name, Options)` exists for the one case that needs it, a splitter
chosen by configuration, and the `split` example uses it once.

### Genuinely missing, and small

These have no Go counterpart and no interface waiting for one. They are listed
because "partial" above should be checkable.

- `normalize` has no entity, date or number normalizer and no language
  detector, though its package comment refers to `Entity`, `Date`, `Number`,
  `Measure` and `Rows` as if it did. Encoding detection and the text transforms
  are all present.
- `split` has no entity-aware, relation-aware, table or hierarchical chunker.
  Sliding windows are `Chars` with `Overlap`; structural splitting is
  `Markdown` and `Code`.
- `prov` declares the PROV-O types and the `Log` interface and ships no
  implementation, so the `prov` example writes one — forty lines. There is no
  hash-chained tamper-evidence, which notebook 22 demonstrates.
- Change management (notebook 24) is halfway. Bitemporal spans, retraction,
  purge with a tombstone, and an audit trail are all in `agent` and are what
  `temporal` shows. Versioned snapshots with checksums and named release tags
  are not: `store.File` has `Dump` and `Load`, and `agent.Rules` versions
  policies, but nothing versions a whole graph.
- The semantic layer (notebook 26) infers an ontology from a graph, which
  `ontology` shows, but there is no mapping layer that binds graph terms to
  ontology terms and applies the binding.
- Seed data (notebook 25) has no `SeedDataManager`. Reading a roster from CSV
  and folding it into a graph is `ingest` plus `kg.Build`, which is what
  `pipeline` does.
- A claim has two identifiers that are not the same string: `kg.Hash` names it
  for the graph and `reason.Key` names it for a derivation. The `prov` example
  keys its log by the second so the reasoner's own PROV record joins it, and
  prints the first beside each claim.

## Things the examples show that the Python does not

Not everything here is a subset.

- `ingest.Web` refuses an internal address by default — loopback, private,
  link-local, cloud metadata — and checks again on the socket the connection
  lands on. The `ingest` example shows the refusal and the one field that opts
  out of it for a trusted deployment.
- Every splitter's chunks tile the document exactly with no overlap. The
  `split` example checks that rather than asserting it.
- `dedupe.Merge` under the `last` strategy picks the later record as the
  survivor and then takes the earlier record's value where the two disagree.
  The `dedupe` example prints all five strategies side by side, which is where
  that shows up.
- `store.Mem.Metric()` reports `""` on a store nobody configured, though it
  ranks as cosine. The `store` example prints what it actually returns.
- `ontology.CheckProperty` normalizes a PascalCase property by lowering the
  whole word, so `WorksFor` is suggested as `worksfor`. The `ontology` example
  prints the suggestion it gets.
