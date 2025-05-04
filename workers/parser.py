from abc import ABC, abstractmethod
from typing import Any

class Parser(ABC):
    @abstractmethod
    def parse(self, data: Any) -> str:
        raise NotImplementedError("Subclasses must implement parse method")
    def chunk(self, text: str) -> list[str]:
        raise NotImplementedError("Subclasses must implement chunk method")
    @abstractmethod
    def supported_types(self) -> list[str]:
        raise NotImplementedError("Subclasses must implement supported_types method")
