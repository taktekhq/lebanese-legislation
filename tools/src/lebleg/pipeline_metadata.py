"""Small managed dataset-card blocks, leaving attribution and YAML untouched."""

START = '<!-- lebleg-release:start -->'
END = '<!-- lebleg-release:end -->'


def update_block(text, body):
    block = START + '\n' + body.strip() + '\n' + END
    if START in text or END in text:
        if text.count(START) != 1 or text.count(END) != 1 or text.index(END) < text.index(START):
            raise ValueError('invalid managed dataset block')
        return text[:text.index(START)] + block + text[text.index(END) + len(END):]
    lines = text.splitlines(keepends=True)
    for i, line in enumerate(lines):
        if line.startswith('# '):
            lines.insert(i + 1, '\n' + block + '\n')
            return ''.join(lines)
    raise ValueError('dataset card has no heading')
