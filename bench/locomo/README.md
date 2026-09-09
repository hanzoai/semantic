# LoCoMo

LoCoMo asks what a system remembers of a conversation held over months. Ten
conversations, 272 sessions, 5,882 turns, 1,986 questions, and the questions are
typed: single-hop, multi-hop, temporal, open-domain, and adversarial — questions
built to look answerable when they are not.

Two memories are built here over the same turns and asked the same questions by
the same reader, marked by the dataset's own scorer.

- **vector** — every turn as a weighted bag of stemmed terms in `store.Mem`,
  retrieved by cosine. This is how the benchmark is usually attempted.
- **graph** — every turn read for who did what, then indexed under the person it
  was read as being about. Retrieval is still cosine over `store.Mem`; what
  changes is that the question's named subject filters the candidates first.

That second description is deliberate and was corrected after review. An earlier
version of this file said retrieval here is "a lookup on a node, not a nearest
neighbour." That was false, and the ablation is one line: empty the `kg` graph
entirely and every number below is byte-identical. The graph is where the read
turns are *kept*; it is not what answers a question. The measured effect comes
from scoping retrieval by subject, not from graph structure and not from
inference — `reason` derives 44 facts from 23,942, which is decoration at this
scale.

Two more columns keep the first two readable. **graph+edge** is the same graph
read differently: the answer comes off the matched edge rather than out of the
turn it was read from. **oracle** is handed the turns the dataset itself names
as the evidence — perfect retrieval, everything else unchanged — so the distance
between an arm and the oracle is what better remembering is still worth, and the
distance from the oracle to 1.0 is what the reader costs everybody equally.

## The result

    go run ./bench/locomo

```
k=5 floor=0.30 span=4 band=0.90 parts=2

                               graph            graph+edge                oracle                vector
category           n      F1 recall  quiet      F1 recall  quiet      F1 recall  quiet      F1 recall  quiet
single hop       841   0.121  0.507    24%   0.056  0.507    21%   0.199  1.000    42%   0.118  0.546    20%
multi hop        282   0.020  0.228    18%   0.025  0.228    17%   0.068  0.973    44%   0.023  0.194    17%
temporal         321   0.338  0.532    23%   0.336  0.532    22%   0.379  0.997    33%   0.357  0.581    16%
open domain       96   0.018  0.238    56%   0.018  0.238    55%   0.032  0.951    79%   0.016  0.258    45%
adversarial      446   0.502  0.135    50%   0.484  0.135    48%   0.426  1.000    43%   0.200  0.511    20%
answerable      1540   0.142  0.444    25%   0.107  0.444    23%   0.202  0.991    43%   0.144  0.471    20%
overall         1986   0.223  0.375    31%   0.191  0.375    29%   0.252  0.993    43%   0.156  0.480    20%
```

`F1` is the official metric. `recall` is the share of a question's evidence
turns the memory returned, which owes nothing to the reader. `quiet` is how
often it declined to answer.

**Read the `overall` row as arithmetic, not as a result.** A system that replies
"No information available" to all 1,986 questions scores 0.228 on this metric,
which beats every arm in the table. A quarter of the questions are adversarial
and the scorer rewards declining them, so any aggregate over the whole set
rewards silence. The two halves have to be read apart, and only the split below
means anything.

The two memories are level on the 1,540 answerable questions — 0.142 against
0.144, bootstrap CI over the ten conversations [-0.010, +0.006] — and separate
on the 446 adversarial ones, 0.502 against 0.200, CI [+0.248, +0.347]. The
graph declined on half the adversarial questions and a quarter of the
answerable ones. The vector index declined on a fifth of each. **It declines at
the same rate on both because it cannot tell them apart**: its abstention
carries no signal about whether a question is answerable at all (ROC AUC 0.520,
which is chance), where the subject-scoped index's carries some (0.684).

Two limits on how far that separation generalizes. About half of it is the
subject filter alone rather than anything read from the turns — adding only the
subject partition to the unchanged baseline turn index gets 0.354 adversarial,
and the claim-span index with the filter removed scores 0.170, below the flat
baseline. And "level on answerable" is weaker than it sounds: the extractive
reader tops out at 0.306 there even with perfect retrieval, so that column is
nearly insensitive to retrieval quality and could move once a model reader sits
behind both arms.

## Why the adversarial questions separate them

An adversarial question in LoCoMo is not nonsense. It is a question about the
wrong person. From `conv-43`:

> **D20:2** John: *Congrats on your success! I'm also trying out yoga to get a
> little extra strength and flexibility.*
>
> **Q** What is Tim trying out to improve his strength and flexibility after
> recovery from ankle injury?

John does yoga. Tim asked him about it. The reference answer is that the
conversation does not say. What the run produced:

```
vector     : 'yoga get little extra'          ctx=[D20:2 D20:4 D7:16 D19:1 D19:6]
graph      : 'No information available'       ctx=[D7:16 D9:9 D18:9 D6:4 D18:13]
oracle     : 'yoga get little extra'          ctx=[D20:2]
```

D20:2 is the nearest turn in the conversation to those words, and the vector
index is right about that and wrong about the question. The graph never sees
D20:2, because D20:2 is not a turn about Tim; the sentence carrying yoga has
John as its subject, so the edge hangs off John. Asked about Tim, the graph
finds nothing above the floor and says so.

The oracle answers wrongly for the same reason as the vector index, from better
evidence. Perfect retrieval is a liability on an adversarial question: the trap
turn *is* the annotated evidence.

## What each index can reach at all

Run with the top-k lifted past the number of turns and the recall column stops
measuring ranking and starts measuring reach — what the index is capable of
returning, however hard you ask.

    go run ./bench/locomo -k 2000 -floor 0

```
category         n    graph    vector
single hop     841    0.787     0.995
multi hop      282    0.688     0.985
temporal       321    0.785     0.997
open domain     96    0.584     0.966
adversarial    446    0.269     0.964
```

The flat index reaches essentially everything. The graph reaches 58–79% of the
evidence for questions that have an answer, and 27% for questions that do not.
It is selectively blind, and blind in the right place. The 21–42% it loses on
answerable questions is the cost of a rule-based extractor: a sentence whose
subject it fails to resolve produces no edge for that person, and the turn
becomes unreachable through them.

## The trade, at matched silence

Declining to answer is worth a point on an adversarial question and costs one
everywhere else, so any comparison at a single threshold is arguable. The
threshold is swept instead.

    go run ./bench/locomo -sweep floor

```
                               graph                  graph+edge                      oracle                      vector
   floor     1-4    adv    all quiet     1-4    adv    all quiet     1-4    adv    all quiet     1-4    adv    all quiet
       0   0.152  0.047  0.129    5%   0.117  0.016  0.094    2%   0.306  0.045  0.247    3%   0.149  0.092  0.136    8%
     0.1   0.152  0.070  0.134    6%   0.117  0.038  0.099    3%   0.270  0.182  0.251   19%   0.149  0.101  0.138    9%
     0.2   0.151  0.217  0.166   13%   0.114  0.195  0.133   11%   0.238  0.285  0.248   30%   0.148  0.132  0.145   12%
     0.3   0.142  0.502  0.223   31%   0.107  0.484  0.191   29%   0.202  0.426  0.252   43%   0.144  0.200  0.156   20%
     0.4   0.126  0.693  0.253   48%   0.094  0.682  0.226   47%   0.164  0.554  0.251   55%   0.131  0.323  0.174   33%
     0.5   0.103  0.845  0.270   63%   0.078  0.841  0.249   62%   0.121  0.697  0.251   67%   0.115  0.491  0.199   48%
     0.6   0.080  0.948  0.275   75%   0.058  0.946  0.257   74%   0.095  0.791  0.251   75%   0.092  0.668  0.222   62%
     0.8   0.038  0.975  0.248   91%   0.030  0.973  0.242   90%   0.042  0.922  0.240   91%   0.043  0.879  0.231   86%
```

Reading across for equal `quiet` — each arm at whichever floor makes it as
silent as the other:

| silence | graph 1–4 | graph adv | vector 1–4 | vector adv |
|---------|-----------|-----------|------------|------------|
| ~12%    | 0.151     | 0.217     | 0.148      | 0.132      |
| ~32%    | 0.142     | 0.502     | 0.131      | 0.323      |
| ~48%    | 0.126     | 0.693     | 0.115      | 0.491      |
| ~62%    | 0.103     | 0.845     | 0.092      | 0.668      |

(graph at floors 0.2, 0.3, 0.4, 0.5 against vector at 0.2, 0.4, 0.5, 0.6.)

At every rate of declining, the graph is ahead of the flat index on both halves
of the benchmark. Silence alone does not buy this: either arm can be made to
fall silent as often as you like, and only one of them falls silent on the
right questions.

## Where the graph does not win

**Multi-hop.** The expectation going in was that multi-hop favours a graph,
since one person's several answers were said months apart and are one node's
several edges here. Retrieval bears that out — recall 0.228 against 0.194 at
k=5, and 0.419 against 0.388 under `-k 20 -parts 4 -band 0.5`, the settings
that let an answer be a list. F1 does not move with it: 0.020 against 0.023,
and 0.053 against 0.057. Gathering the turns is not the hard part of a
multi-hop answer; assembling four phrases from four sessions into one list is,
and an extractive reader cannot. The oracle scores 0.068 with perfect evidence,
which is the ceiling this reader puts on the category.

**Answering off an edge.** `graph+edge` reads the answer from the matched edge —
the object phrase the extractor produced — instead of quoting the turn. It is
worse on single-hop by more than half, 0.056 against 0.121. The extracted object
is a lossy paraphrase of the sentence, and the sentence is what the reference
answer was written from. The graph is a better *index* than it is a *store of
answers*, at least when the extractor is rules rather than a model.

**Inference.** `reason` runs over the extracted triples with one rule, that what
a thing is called is also what its owner has. Across all ten conversations it
derived 44 facts from 23,942. Conversational memory is dominated by aggregation
over node identity — the same person mentioned in two sessions is one node,
which needs no inference at all — and there is very little left for a reasoner
to add. That is a finding, not a defect: it says where the value in this kind of
graph actually sits.

**Absolute numbers.** Everything here is low. The reader is deterministic and
extractive: it picks the sentence covering most of the question, drops the words
the question already contained, and keeps the rarest few of what is left. No
model is called. The oracle row bounds every arm — 0.306 on answerable questions
with perfect retrieval and no abstention — and that bound is the reader's, not
the memories'. A model reader would raise every column; it would also make the
comparison a comparison of two prompts.

## How it is put together

```
locomo.go   the dataset: sessions in order, dates parsed, evidence ids
talk.go     reading dialogue: who a sentence is about, and what it claims
graph.go    the graph arm: claims -> kg -> reason -> a store filtered by person
vector.go   the baseline: one weighted vector per turn
oracle.go   the arm that cheats, to bound the reader
terms.go    one vocabulary, weighed once, shared by everything
read.go     the reader, shared by every arm
metric.go   the official metric, ported
porter.go   the stemmer the official metric runs
bench.go    the experiment and the table
```

**Reading dialogue.** The library's `extract.Rules` knows how to find a company
being founded; a friend saying *"I went to a support group yesterday"* is a
different grammar. `talk.go` is its reader, and one observation does most of the
work: in a two-party conversation the subject of a sentence is settled by who is
speaking. *I* is the speaker, *you* is the other one, a name is that person, and
a sentence naming nobody is about the speaker. Two rules keep false claims out,
and both are grammar rather than tuning:

- A question asserts nothing. *"How's it going with yoga?"* does not make the
  asker someone who does yoga. This is what stops a topic leaking from the
  person who raised it to the person who answered, and it is exactly what the
  adversarial category punishes.
- A negation is kept. *"I don't eat dairy"* is stored with the negation on the
  predicate, so it is findable and is not mistaken for its opposite.

98.9% of LoCoMo questions name one of the two speakers, which is what makes a
lookup on a person the right query for them. The 1.1% that name nobody fall back
to searching every piece.

**What is indexed.** The baseline indexes turns. The graph indexes *pieces* — what
one person said in one turn — so a turn holds as many pieces as it has people
spoken of, and a question reaches only the pieces of the person it names. The
claims decide which sentences belong to a piece; the sentences are what it is
ranked on. Ranking on the extracted claims alone would measure how well the
extractor paraphrases rather than what the graph knows about whom.

Both arms rank by cosine over the same weighted terms through `store.Mem`, and
the subject restriction is a metadata filter (`store.Filter{}.Eq(store.Space,
who)`) rather than a second store. The comparison is therefore an ablation of
the index, not of two systems.

**Confidence** is computed once, in `Sure`, outside the arms: how much of the
question's terms the best turn a memory returned accounts for, weighted by how
rare each term is. Computing it over turns rather than over whatever an arm
happens to index is what keeps the number meaning the same thing in every
column — a memory indexing short pieces would otherwise look less sure of the
same evidence, and the report would be measuring text length.

**The reader** never sees the question's category and never sees which arm
produced the evidence. It reads a date off the session for a *when* question,
resolving a month named in the text against it — *"back in October"* in a May
session is the previous October — and otherwise quotes the sentence. Its three
settings barely matter: swept over all ten conversations, `span` moves the
graph's overall score from 0.215 to 0.224, `band` from 0.220 to 0.223 and
`parts` from 0.220 to 0.223. The two settings that do matter, `k` and `floor`,
both trade answering against declining, and both are swept above.

## The metric is the official one

`metric.go` and `porter.go` port `task_eval/evaluation.py` and the NLTK stemmer
it calls. That port is held against the Python three ways:

- `TestStem` — NLTK's stem for all 6,597 words in the corpus.
- `TestNormalise` — the official `normalize_answer` over all 10,382 distinct
  questions, answers and turns.
- `TestGrade` — `eval_question_answering`'s score for 2,400 replies spanning
  every category and every branch of the metric.

And then end to end: `score.py` runs the official Python over the same
predictions file the Go wrote.

    make check

```
                   n                 graph            graph+edge                oracle                vector
category                  F1 recall             F1 recall             F1 recall             F1 recall
single hop       841   0.121  0.507          0.056  0.507          0.199  1.000          0.118  0.546
multi hop        282   0.020  0.228          0.025  0.228          0.068  0.973          0.023  0.194
temporal         321   0.338  0.532          0.336  0.532          0.379  0.997          0.357  0.581
open domain       96   0.018  0.238          0.018  0.238          0.032  0.951          0.016  0.258
adversarial      446   0.502  0.135          0.484  0.135          0.426  1.000          0.200  0.511
answerable      1540   0.142  0.444          0.107  0.444          0.202  0.991          0.144  0.471
overall         1986   0.223  0.375          0.191  0.375          0.252  0.993          0.156  0.480
```

Every figure matches the Go to three decimals, across 1,986 questions and four
arms.

Two details of the official scorer are worth stating, because they shape what
the numbers mean. An adversarial answer is scored by looking for the phrases
*no information available* or *not mentioned* in the reply — the reference
answer is not consulted. And a multi-hop answer is scored by splitting both
sides on commas and giving each reference part the best-matching reply part, so
recall is rewarded and a wrong extra part costs nothing. `parts` is bounded at 2
here for that reason; the bound is ours, not the scorer's.

## Running it

```sh
go run ./bench/locomo                        # the table above
go run ./bench/locomo -show 5                # answers, side by side, per category
go run ./bench/locomo -sweep floor           # or k, span, band, parts
go run ./bench/locomo -k 2000 -floor 0       # what each index can reach
go run ./bench/locomo -out bench/locomo/out/answers.json
go test ./bench/locomo/
```

Run from the repository root, and with `GOWORK=off` if a parent `go.work` is in
the way. Reading ten conversations into both memories and answering 1,986
questions four ways takes 5.2s of CPU (10.8s wall on a busy laptop), measured with
`/usr/bin/time -l`.

The dataset is fetched on first use, through the library's own `ingest.Web`, to
`bench/locomo/data/locomo10.json` — 2,805,274 bytes, SHA-256
`79fa87e90f04081343b8c8debecb80a9a6842b76a7aa537dc9fdf651ea698ff4`. Take it from
GitHub and not from HuggingFace: `adymaharana/locomo` there holds a README and
`.gitattributes` and no data at all, which is a good way to lose an afternoon.

For the Python cross-check:

```sh
cd bench/locomo
make golden    # writes the two golden files that quote the dataset
make check     # scores out/answers.json with the official evaluation.py
```

Both fetch what they need. LoCoMo is CC BY-NC 4.0 and this repository is MIT, so
neither the dataset nor `task_eval/evaluation.py` is kept here; `.gitignore`
lists what is fetched. `testdata/stems.tsv` is a word list and stays.

## What was built, per conversation

```
conv-26     419 turns   1230 terms    1822 claims   1226 nodes    1682 edges     1 derived
conv-30     369 turns   1010 terms    1482 claims    928 nodes    1301 edges     0 derived
conv-41     663 turns   1389 terms    2714 claims   1650 nodes    2434 edges     3 derived
conv-42     629 turns   1390 terms    2394 claims   1510 nodes    2144 edges     8 derived
conv-43     680 turns   1539 terms    2931 claims   1752 nodes    2529 edges     0 derived
conv-44     675 turns   1345 terms    2964 claims   1636 nodes    2578 edges     7 derived
conv-47     689 turns   1543 terms    2664 claims   1685 nodes    2451 edges    22 derived
conv-48     681 turns   1491 terms    2417 claims   1575 nodes    2182 edges     3 derived
conv-49     509 turns   1336 terms    1899 claims   1310 nodes    1725 edges     0 derived
conv-50     568 turns   1274 terms    2655 claims   1538 nodes    2331 edges     0 derived
```

## Caveats

The vector arm weights terms lexically — inverse document frequency, stemmed,
unit length, cosine — rather than with a learned embedding, because nothing here
reaches a network. On this data that is the harder baseline, not the easier one:
LoCoMo questions are written from the turns they ask about and repeat their
words, so lexical overlap is strong. A dense retriever generalises across
wording. It does not fix the failure this benchmark turns on, which is that a
turn near the question's words is not the same thing as a turn about the person
the question names — that is a property of what is indexed, not of how the
vectors are made.

No LLM was called, in either arm or in the reader. Absolute F1 is therefore well
below published model numbers on this benchmark and is not comparable to them.
What is comparable is the two columns against each other, since they differ in
exactly one thing.
