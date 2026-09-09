"""The official LoCoMo scorer, fetched rather than vendored.

snap-research/locomo is CC BY-NC 4.0 and this repository is MIT, so their
evaluation.py is downloaded on first use and kept out of git. It is imported
unmodified: the one thing done to it is standing a stub in for the bert_score
import it makes at module scope and never uses in eval_question_answering.
"""

import sys
import types
import urllib.request
from pathlib import Path

HERE = Path(__file__).parent
SOURCE = "https://raw.githubusercontent.com/snap-research/locomo/main/task_eval/evaluation.py"


def official():
    """The path to evaluation.py, downloading it if this is the first ask."""
    path = HERE / "evaluation.py"
    if not path.exists():
        print(f"fetching {SOURCE}", file=sys.stderr)
        with urllib.request.urlopen(SOURCE) as response:
            path.write_bytes(response.read())
    return path


official()

if "bert_score" not in sys.modules:
    stub = types.ModuleType("bert_score")
    stub.score = None
    sys.modules["bert_score"] = stub

sys.path.insert(0, str(HERE))
from evaluation import eval_question_answering, f1, f1_score, normalize_answer  # noqa: E402,F401
