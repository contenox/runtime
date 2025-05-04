from typing import Any
from workers import parser

class TextParser(parser.Parser):
    def __init__(self):
        super().__init__()

    def parse(self, raw_data: Any) -> str:
        """
        Parses raw byte data as plain text.
        """
        parsed_text = raw_data.decode('utf-8', errors='replace')
        return parsed_text

    def chunk(self, text: str) -> list[str]:
        """
        Splits text into chunks of max_length.
        """
        chunks = []
        for i in range(0, len(text), 256):
            chunks.append(text[i:i+256])
        return chunks

    def supported_types(self) -> list[str]:
        return ['vectorize_text/plain; charset=utf-8']
