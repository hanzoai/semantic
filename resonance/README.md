# Phase-aware retrieval in Hanzo Semantic (experimental)

This repository is the **semantic memory and knowledge layer**. Hanzo Engine
runs embeddings/inference and optionally accelerates scoring; it does not own a
second independent long-term memory database.

The new `resonance` package implements a standalone, standard-library Go
complex-vector similarity kernel and optional candidate reranker, based on the
published equation in arXiv:2509.09691v1. The same math has an experimental
Rust implementation in [Engine PR #39](https://github.com/hanzoai/engine/pull/39).

Neither experimental kernel is wired to the production retrieval path yet.
Existing `store.Vector`, `store.Graph`, `store.Triple`, `agent.Memory`,
`dedupe`, and the LoCoMo benchmark are unaffected.

## Architecture

    ingest -> parse -> extract -> normalize proposition + source
       -> kg / triple / agent memory (subject, scope, time, provenance)
       -> existing store.Vector embedding index (candidate recall)
    query -> extract subject, relation, polarity, epistemic stance, time
       -> filter tenant/ACL/source/subject/time as appropriate
       -> store.Vector / BM25 candidates (50-200 for evaluation)
       -> resonance.Rank optional phase-aware shortlist reranker
       -> contradiction/provenance resolution (dedupe + reason)
       -> cited, scoped memory context for Kai/Enso/Kata

The existing split between vector similarity, graph traversal, and triples is
valuable: resonance is a **fourth score**, not a fourth storage backend.

Do not perform a phase rerank by globally flipping embeddings for arbitrary
sentences. Negation is scoped to **atomic propositions**:
`(subject, predicate, object, time, modality, provenance)`.
Claims like `Alice did not say Bob resigned` are NOT the anti-phase of
`Bob resigned`. Quote attribution, modality, contradictory sources and temporal
transitions require explicit structured handling.

## Candidate flow

- Retain filtered dense/vector/lexical retrieval for recall.
- Use `store.Query.Filter` and the subject-scoping model that already improved
  adversarial LoCoMo performance to narrow candidates before phase reranking.
- Obtain the original dense cosine from the preexisting retrieval result.
- Obtain a per-candidate wave from the explicit phase channel stored with an
  assertion, or from a versioned phase encoder. The ordinary `resonance.Embed`
  wrapper supplies no new semantic polarity.
- Run `resonance.Rank(query, candidates, weight, k)` only on retrieved
  candidates. Return the score components for observability.
- Resolve real contradictions through `dedupe`, source trust, validity time,
  and `reason`; interference alone does not establish which claim is true.
- Fall back to baseline ranking when no grounded phase metadata exists; don't
  manufacture opposite phases.

## Implementation boundary

- `hanzoai/semantic/resonance`: semantics-level reference implementation,
  unit vectors, experimental pure-Go shortlisted scoring.
- `hanzoai/semantic/store`: retrieval adapters and metadata filters remain
  stable; future opt-in adapter will supply hydrated phase metadata.
- `hanzoai/engine/hanzo-engine/src/resonance.rs`: optional native Rust score
  accelerator with parity checks against the Go reference.
- `hanzoai/semantic/agent` and `kg`: scope, timeline and source identity
  continue to own memory and provenance.

No new external Go dependency; no upstream ResonanceDB code imported.

## Measurement before rollout

Use `bench/locomo` and a contrastive negation benchmark together. LoCoMo's
documented improvements are predominantly from **subject scoping and spans**,
NOT graph inference. The existing overall F1 is also strongly influenced by
abstention, so report answerable and adversarial categories separately.

Compare:
1. Existing vector-only and subject-scoped/span retrieval.
2. Structured metadata polarity filters without phase.
3. Dense + resonance, with fixed candidate set and same metadata filters.
4. Dense + cross-encoder or entailment reranker.
5. Dense + resonance + provenance/time/contradiction checks.

Evaluate answerable accuracy, adversarial false answers, scoped-negation
minimal pairs, Recall@20/100, nDCG@10, abstention calibration,
p50/p95/p99 CPU latency, index/ingestion costs, and 10K/100K/1M collections.

**Promotion rule:** demonstrate held-out quality improvement at comparable
recall and latency, on unseen queries, versus the structured-polarity baseline.
A perfect anti-phase toy case is not evidence of language understanding.

## Status

Native Go scorer with tests; an optional reranker callable by applications.
No automatic phase extractor, no memory-store persistence schema for phases,
no HTTP service, no CLI integration, no production flag and no LoCoMo
resonance run yet. Merge only after full CI and compatibility validation.
