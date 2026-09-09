# The benchmark, and the cross-check against the Python it was ported from.
#
#   make            the table
#   make sweeps     how the answer moves with every setting
#   make stats      constant abstention, the AUCs, the intervals
#   make check      the same predictions, scored by the official Python
#   make golden     the files the metric tests hold themselves against

GO = GOWORK=off go
ROOT = ../..
OUT = out/answers.json
CELLS = out/factor.json

.PHONY: bench sweeps test stats check golden venv clean

bench: $(OUT)

$(OUT):
	cd $(ROOT) && $(GO) run ./bench/locomo -out bench/locomo/$(OUT)

$(CELLS):
	cd $(ROOT) && $(GO) run ./bench/locomo -factor -out bench/locomo/$(CELLS)

sweeps:
	cd $(ROOT) && for s in floor k span band parts; do $(GO) run ./bench/locomo -sweep $$s; done

test:
	cd $(ROOT) && $(GO) test ./bench/locomo/

# What a table of means cannot show, read back out of both predictions files.
stats: $(OUT) $(CELLS)
	cd $(ROOT) && $(GO) run ./bench/locomo -stats bench/locomo/$(OUT)
	cd $(ROOT) && $(GO) run ./bench/locomo -stats bench/locomo/$(CELLS)

# The official scorer over the same answers, both arm sets. It fetches
# evaluation.py itself: LoCoMo is CC BY-NC and this repository is MIT, so none
# of it is kept here.
check: venv $(OUT) $(CELLS)
	.venv/bin/python score.py $(OUT)
	.venv/bin/python score.py $(CELLS)

# The golden files the metric tests hold themselves against. Two of them quote
# the dataset, so they are written here rather than kept in the repository.
golden: venv
	.venv/bin/python ref/golden.py

venv:
	@test -d .venv || uv venv --python 3.11 .venv
	@test -f .venv/bin/nltk || (VIRTUAL_ENV=$(PWD)/.venv uv pip install nltk regex numpy && touch .venv/bin/nltk)

clean:
	rm -rf out
