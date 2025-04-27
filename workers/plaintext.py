from typing_extensions import Any
from workers import Parser

class Text_Parser(Parser.Parser):
    def __init__(self):
        super().__init__()

    def parse(self, job_id: str, raw_data: Any) -> str:
        """
        Parses raw byte data as plain text.
        """
        parsed_text = raw_data.decode('utf-8', errors='replace')
        return parsed_text

    def supported_types(self) -> list[str]:
        return ['text/plain']
