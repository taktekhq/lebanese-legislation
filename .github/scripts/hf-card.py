#!/usr/bin/env python3
"""Write the dataset card: this repository's card, keeping the release block
(between the lebleg-release markers) of the card now on Hugging Face, which
the release pipeline maintains.

    hf-card.py CARD LIVE_CARD OUT
"""
import sys

START, END = "<!-- lebleg-release:start -->", "<!-- lebleg-release:end -->"


def main(card_path, live_path, out_path):
    card = open(card_path, encoding="utf-8").read()
    try:
        live = open(live_path, encoding="utf-8").read()
    except FileNotFoundError:
        live = ""
    if START in live and END in live and live.index(START) < live.index(END):
        block = live[live.index(START):live.index(END) + len(END)]
        if START in card:
            card = card[:card.index(START)] + block + card[card.index(END) + len(END):]
        else:
            lines = card.splitlines(keepends=True)
            i = next(i for i, l in enumerate(lines) if l.startswith("# "))
            lines.insert(i + 1, "\n" + block + "\n")
            card = "".join(lines)
    open(out_path, "w", encoding="utf-8").write(card)


if __name__ == "__main__":
    main(*sys.argv[1:4])
