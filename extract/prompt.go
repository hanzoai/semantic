package extract

// The prompts LLM sends. Each states the JSON shape of the reply, shows one
// example of it, and then names the text to work from — in that order,
// because a model told the shape after the text tends to answer in prose.
//
// The type and predicate instructions are separate constants rather than
// inline branches: what a caller may steer (which labels, which predicates,
// whether to ask for time) is exactly the part that varies, and keeping it
// apart from the fixed scaffolding is what lets the scaffolding be one string.
//
// These follow semantica/semantic_extract/methods.py, which is where the
// wording was actually tuned against real providers.

// entPrompt asks which entities a text names. The holes are the type
// instruction and the text.
const entPrompt = `Extract named entities from the provided text.
Return the result as a JSON object with an "entities" key containing the list of entities.
Each entity should have 'text', 'label', and 'confidence' fields.

IMPORTANT:
- Return a FLAT LIST of entities.
- DO NOT group entities by type.
- The output structure must exactly match: { "entities": [ { "text": "...", "label": "...", "confidence": ... }, ... ] }

Example output (JSON format only):
{
  "entities": [
    {"text": "Entity Name", "label": "CATEGORY", "confidence": 0.95},
    {"text": "Another Entity", "label": "OTHER_CATEGORY", "confidence": 0.90}
  ]
}

Instructions:
1. Extract entities ONLY from the text provided below.
2. Do not include any entities from the example above.
3. %s

Text to extract from:
%s`

// anyKind is the entity instruction when the caller named no types: the
// general set, with the cases that are decided wrongly most often spelled out.
const anyKind = `Entity types should be one of:
- PERSON (People, names, roles)
- ORG (Companies, organizations, institutions, brands)
- GPE (Countries, cities, states, locations)
- DATE (Dates, years, time periods)
- EVENT (Named events, conferences)
- PRODUCT (Software, hardware, vehicles)
- CONCEPT (Abstract ideas, technologies)

Use the most appropriate type for each entity.
Examples:
- 'Microsoft' is an ORG
- 'Satya Nadella' is a PERSON
- Job titles/roles like 'CEO', 'CTO', 'President', 'Engineer' are CONCEPT unless part of a person's name
- 'Python' is a PRODUCT or CONCEPT depending on context.`

// someKind is the entity instruction when the caller named types. They are a
// preference, not a closed vocabulary: a model forced to squeeze an entity
// into the nearest listed type mislabels it, where a near neighbour is at
// least honest and can be reconciled later. A caller who does want the
// vocabulary closed sets Schema, which refuses what leaves it.
const someKind = `Preferred entity types: %s.
You may also use related or similar entity types if they better match the context (e.g., variations, synonyms, or domain-specific types).
If an entity doesn't fit any of the preferred types, use the most appropriate type from the preferred list or a closely related type.`

// relPrompt asks how the entities in a text are connected. The holes are the
// predicate instruction, the text, and the entity roster.
const relPrompt = `Extract relations between entities from the provided text.
Return the result as a JSON object with a "relations" key containing the list of relations.
Each relation must have 'subject', 'predicate', and 'object' fields.

Example output (JSON format only):
{
  "relations": [
    {"subject": "Entity A", "predicate": "related_to", "object": "Entity B", "confidence": 0.95},
    {"subject": "Subject Entity", "predicate": "action_verb", "object": "Object Entity", "confidence": 0.90}
  ]
}

Instructions:
1. Extract relations ONLY from the text provided below.
2. Do not include any relations from the example above.
3. Use the provided entities list as a reference for subjects and objects.
4. %s

Text to extract from:
%s
Entities found in text: %s`

// whenPrompt is relPrompt plus the question of when each relation held. It
// carries a calibration table because a model asked for a confidence without
// one returns 0.9 for everything, and few-shot pairs because the rule that
// matters most — say nothing rather than guess a date — is learned from
// examples and not from being told. The holes are the same three as relPrompt.
const whenPrompt = `Extract relations between entities from the provided text, along with temporal validity information for each relation.
Return the result as a JSON object with a "relations" key. Each relation must have:
'subject', 'predicate', 'object', 'confidence', 'valid_from', 'valid_until', 'temporal_confidence', 'temporal_source_text'.

TEMPORAL EXTRACTION RULES:
- valid_from: ISO 8601 date or exact phrase from the text for when this relation became valid. Set to null if no temporal signal is present.
- valid_until: ISO 8601 date or exact phrase for when this relation ceased. Set to null if open-ended or absent.
- temporal_confidence (float 0.0-1.0) - calibrated as follows:
    1.00 = full ISO date ("2022-03-15", "March 15, 2022")
    0.90 = explicit year + month ("March 2022", "2022-03")
    0.85 = explicit year only ("in 2022", "since 2021", "from 2019")
    0.75 = quarter ("Q3 2023", "Q2 2021")
    0.65 = named season or approximate range ("summer 2022", "early 2020s", "mid-2022")
    0.50 = vague relative with computable anchor ("last year", "three months ago")
    0.35 = highly vague relative ("recently", "years ago", "in the past")
    0.00 = no temporal signal present for this relation
- temporal_source_text: the EXACT verbatim substring from the source text that contains the temporal signal. Set to null when temporal_confidence is 0.0.

IMPORTANT: Do NOT invent or guess dates. If the text contains no temporal signal for a relation, set valid_from and valid_until to null and temporal_confidence to 0.0.

Few-shot examples (do NOT include these in your output):
  Text: "Apple acquired Beats in May 2014."
  -> valid_from: "2014-05-01", valid_until: null, temporal_confidence: 0.90, temporal_source_text: "May 2014"

  Text: "The CEO has led the company since Q3 2020."
  -> valid_from: "Q3 2020", valid_until: null, temporal_confidence: 0.75, temporal_source_text: "since Q3 2020"

  Text: "Last year, Google partnered with Samsung."
  -> valid_from: "last year", valid_until: null, temporal_confidence: 0.50, temporal_source_text: "Last year"

  Text: "The firm was under enhanced supervision between Q2 and Q4 2021."
  -> valid_from: "Q2 2021", valid_until: "Q4 2021", temporal_confidence: 0.75, temporal_source_text: "between Q2 and Q4 2021"

  Text: "Microsoft develops Windows."
  -> valid_from: null, valid_until: null, temporal_confidence: 0.00, temporal_source_text: null

Example JSON output format:
{
  "relations": [
    {
      "subject": "Apple", "predicate": "acquired", "object": "Beats",
      "confidence": 0.97,
      "valid_from": "2014-05-01", "valid_until": null,
      "temporal_confidence": 0.90, "temporal_source_text": "May 2014"
    }
  ]
}

Instructions:
1. Extract relations ONLY from the text provided below.
2. Do not include any relations from the examples above.
3. Use the provided entities list as a reference for subjects and objects.
4. %s

Text to extract from:
%s
Entities found in text: %s`

// anyPred is the relation instruction when the caller named no predicates.
const anyPred = `
Extract meaningful relationships between entities. Use appropriate relation types that accurately describe how entities are connected.
Common relation types include: related_to, part_of, located_in, created_by, uses, depends_on, interacts_with, and similar variations.`

// somePred is the relation instruction when the caller named predicates.
const somePred = `
Preferred relation types: %s.
You may also use related or similar relation types if they better capture the relationship (e.g., variations, synonyms, or domain-specific relations).
If a relation doesn't fit any of the preferred types, use the most appropriate type from the preferred list or a closely related type that accurately describes the relationship.`

// triPrompt asks for statements about a text directly, without first naming
// entities. The holes are the predicate instruction and the text.
const triPrompt = `Extract RDF triplets (subject-predicate-object) from the provided text.
Return the result as a JSON object with a "triplets" key containing the list of triplets.
Each triplet must have 'subject', 'predicate', and 'object' fields.

Example output (JSON format only):
{
  "triplets": [
    {"subject": "Subject", "predicate": "predicate_relation", "object": "Object", "confidence": 0.99},
    {"subject": "Concept A", "predicate": "is_a", "object": "Concept B", "confidence": 0.95}
  ]
}

Instructions:
1. Extract triplets ONLY from the text provided below.
2. Do not include any triplets from the example above.
3. Ensure subjects and objects are substrings from the text.
4. %s

Text to extract from:
%s`

// anyTri is the triple instruction when the caller named no predicates.
const anyTri = `
Extract meaningful triplets (subject-predicate-object). Use appropriate predicates that accurately describe the relationship.
Common predicates include: is_a, part_of, has_property, related_to, caused_by, etc.`

// someTri is the triple instruction when the caller named predicates.
const someTri = `
Preferred triplet predicates: %s.
You may also use related or similar predicates if they better capture the relationship (e.g., variations, synonyms, or domain-specific predicates).
If a predicate doesn't fit any of the preferred types, use the most appropriate type from the preferred list or a closely related type that accurately describes the relationship.`
