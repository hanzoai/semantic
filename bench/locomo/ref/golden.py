"""Write the golden files the Go metric tests hold themselves against.

Everything here comes out of the official code in this directory, imported
unmodified through shim.py. Run it from bench/locomo:

    .venv/bin/python ref/golden.py
"""

import json
import random
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
from shim import eval_question_answering, normalize_answer  # noqa: E402
from nltk.stem import PorterStemmer  # noqa: E402

HERE = Path(__file__).parent.parent
OUT = HERE / "testdata"

DATASET = HERE / "data" / "locomo10.json"
if not DATASET.exists():
    raise SystemExit(
        f"{DATASET} is not here yet. Run the benchmark once — "
        "`go run ./bench/locomo` from the repository root — and it fetches it."
    )
DATA = json.load(open(DATASET))


def texts():
    """Every string the metric will ever see: questions, answers, turns."""
    for sample in DATA:
        for qa in sample["qa"]:
            yield qa["question"]
            for key in ("answer", "adversarial_answer"):
                if key in qa:
                    yield str(qa[key])
        for key, value in sample["conversation"].items():
            if re.fullmatch(r"session_\d+", key):
                for turn in value:
                    yield turn["text"]
                    if turn.get("blip_caption"):
                        yield turn["blip_caption"]


def escape(s):
    return s.replace("\\", "\\\\").replace("\t", "\\t").replace("\n", "\\n")


def write_stems():
    stemmer = PorterStemmer()
    vocab = set()
    for text in texts():
        vocab.update(normalize_answer(text).split())
    # The classic Porter test words as well, so the golden covers rules this
    # corpus happens not to exercise.
    vocab.update(
        "hopping flies dies ties sky skies feed agreed plastered bled motoring "
        "sing conflated troubled sized hopped tanned falling hissing fizzed "
        "failing filing happy news innings caresses ponies caress cats "
        "relational conditional rational valenci hesitanci digitizer "
        "conformabli radicalli differentli vileli analogousli triplicate "
        "formative formalize electriciti electrical hopefulness goodness "
        "revival allowance inference airliner gyroscopic adjustable defensible "
        "irritant replacement adjustment dependent adoption homologou communism "
        "activate angulariti homologous effective bowdlerize probate rate cease "
        "controll roll skis lies tying dying outings cannings howe proceed "
        "exceed succeed".split()
    )
    with open(OUT / "stems.tsv", "w") as f:
        for word in sorted(vocab):
            f.write(f"{word}\t{stemmer.stem(word)}\n")
    return len(vocab)


def write_normalise():
    seen = set()
    with open(OUT / "normalise.tsv", "w") as f:
        for text in texts():
            if text in seen:
                continue
            seen.add(text)
            f.write(f"{escape(text)}\t{escape(normalize_answer(text))}\n")
    return len(seen)


def write_grades():
    """Score a spread of replies with the official scorer, category by category.

    A reply that is the answer, a reply that is part of it, a reply that is
    somebody else's answer, an empty reply and a refusal — for every category,
    so each branch of the metric is covered by real data rather than by a case
    invented to pass.
    """
    random.seed(11)
    answers = [
        (qa["category"], str(qa.get("answer", qa.get("adversarial_answer", ""))))
        for sample in DATA
        for qa in sample["qa"]
    ]
    cases = []
    for kind, answer in random.sample(answers, 400):
        other = random.choice(answers)[1]
        half = " ".join(answer.split()[: max(1, len(answer.split()) // 2)])
        for reply in (answer, half, other, "", "No information available", "not mentioned"):
            cases.append({"category": kind, "answer": answer, "prediction": reply})

    scores, _, _ = eval_question_answering(cases, "prediction")
    with open(OUT / "grade.tsv", "w") as f:
        for case, score in zip(cases, scores):
            f.write(
                f"{case['category']}\t{escape(case['prediction'])}"
                f"\t{escape(case['answer'])}\t{score:.12f}\n"
            )
    return len(cases)


print("stems    ", write_stems())
print("normalise", write_normalise())
print("grade    ", write_grades())
