from lebleg.chunks import chunks, digest, reusable


def test_unicode_offsets_and_exact_hash():
    text = 'أ🙂é' * 500
    result = list(chunks(text))
    assert [c.start for c in result] == [0, 780, 1560]
    assert all(c.text == text[c.start:c.end] and c.hash == digest(text) for c in result)
    assert list(chunks(' \t\n')) == []


def test_reuse_checks_full_hash_offsets_and_excerpt():
    text = 'أ🙂' * 500
    chunk = next(chunks(text))
    assert reusable(text, chunk.hash, 0, 900, chunk.text)
    assert not reusable(text + 'x', chunk.hash, 0, 900, chunk.text)
    assert not reusable(text, chunk.hash, 1, 901, chunk.text)
    assert not reusable(text, chunk.hash, 0, 900, 'changed')


def test_go_trimspace_does_not_treat_ascii_file_separator_as_space():
    assert len(list(chunks('\x1c'))) == 1


def test_whitespace_cannot_be_reused_as_a_chunk():
    assert not reusable('\n', digest('\n'), 0, 1, '\n')
