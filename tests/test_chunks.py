from lebleg_starter.chunks import chunks, digest, reusable


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
