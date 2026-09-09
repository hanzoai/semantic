"""Score a predictions file with the official LoCoMo scorer.

The Go harness prints its own numbers; this runs task_eval/evaluation.py over
the same answers so the two can be diffed. They should agree to the last
decimal, and the Go tests hold the port against this Python word for word.

    .venv/bin/python score.py out/answers.json
"""

import json
import sys
from collections import defaultdict
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent / "ref"))
from shim import eval_question_answering  # noqa: E402

NAMES = {4: "single hop", 1: "multi hop", 2: "temporal", 3: "open domain", 5: "adversarial"}
ORDER = [4, 1, 2, 3, 5]


def main(path):
    samples = json.load(open(path))
    arms = sorted(
        k[: -len("_prediction")]
        for k in samples[0]["qa"][0]
        if k.endswith("_prediction")
    )

    score = {a: defaultdict(float) for a in arms}
    reach = {a: defaultdict(float) for a in arms}
    count = defaultdict(int)
    for sample in samples:
        for qa in sample["qa"]:
            count[qa["category"]] += 1
        for arm in arms:
            key = arm + "_prediction_context"
            for qa in sample["qa"]:
                # The official recall reads context[0] to tell a session id
                # from a dialogue id, and a memory that returned nothing has
                # no context[0]. A placeholder that matches no evidence scores
                # the same zero without touching the vendored file.
                if not qa[key]:
                    qa[key] = ["-"]
            marks, _, recall = eval_question_answering(sample["qa"], arm + "_prediction")
            for qa, mark, got in zip(sample["qa"], marks, recall):
                score[arm][qa["category"]] += mark
                reach[arm][qa["category"]] += got

    print(f"\n{'':14}{'n':>6}", end="")
    for arm in arms:
        print(f"  {arm:>20}", end="")
    print(f"\n{'category':14}{'':6}", end="")
    for _ in arms:
        print(f"  {'F1':>6} {'recall':>6}       ", end="")
    print()

    def row(name, kinds):
        n = sum(count[k] for k in kinds)
        print(f"{name:14}{n:6}", end="")
        for arm in arms:
            f1 = sum(score[arm][k] for k in kinds) / n
            rc = sum(reach[arm][k] for k in kinds) / n
            print(f"  {f1:6.3f} {rc:6.3f}       ", end="")
        print()

    for kind in ORDER:
        row(NAMES[kind], [kind])
    row("answerable", [1, 2, 3, 4])
    row("overall", ORDER)


main(sys.argv[1] if len(sys.argv) > 1 else "out/answers.json")
