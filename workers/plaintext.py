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

    def supported_types(self) -> list[str]:
        return ['vectorize_text/plain; charset=utf-8']
