import pytest

from shortener import URLShortener


def test_shorten_returns_consistent_code():
    shortener = URLShortener()

    code1 = shortener.shorten("https://example.com/docs")
    code2 = shortener.shorten("https://example.com/docs")

    assert code1 == code2


def test_expand_returns_original_url():
    shortener = URLShortener()
    url = "https://example.com/learn"

    code = shortener.shorten(url)

    assert shortener.expand(code) == url


def test_expand_unknown_code_raises_keyerror():
    shortener = URLShortener()

    with pytest.raises(KeyError):
        shortener.expand("missing-code")


def test_multiple_urls_get_different_codes():
    shortener = URLShortener()

    code1 = shortener.shorten("https://example.com/a")
    code2 = shortener.shorten("https://example.com/b")

    assert code1 != code2

