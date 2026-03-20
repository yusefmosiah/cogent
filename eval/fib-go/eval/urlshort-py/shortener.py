"""Simple URL shortener backed by in-memory mappings."""

from __future__ import annotations

import hashlib
from dataclasses import dataclass, field

_BASE62_ALPHABET = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"


def _base62_encode(value: int) -> str:
    """Encode a non-negative integer as base62."""
    if value < 0:
        raise ValueError("value must be non-negative")
    if value == 0:
        return _BASE62_ALPHABET[0]

    chars = []
    while value:
        value, remainder = divmod(value, 62)
        chars.append(_BASE62_ALPHABET[remainder])
    return "".join(reversed(chars))


@dataclass
class URLShortener:
    """Generate short codes for URLs and expand them back again."""

    _code_to_url: dict[str, str] = field(default_factory=dict)
    _url_to_code: dict[str, str] = field(default_factory=dict)

    def shorten(self, url: str) -> str:
        """Return a stable short code for ``url``."""
        if url in self._url_to_code:
            return self._url_to_code[url]

        suffix = 0
        while True:
            seed = url if suffix == 0 else f"{url}:{suffix}"
            digest = hashlib.sha256(seed.encode("utf-8")).digest()
            code = _base62_encode(int.from_bytes(digest, "big"))
            if code not in self._code_to_url:
                break
            if self._code_to_url[code] == url:
                break
            suffix += 1

        self._code_to_url[code] = url
        self._url_to_code[url] = code
        return code

    def expand(self, code: str) -> str:
        """Return the original URL for ``code``."""
        return self._code_to_url[code]
