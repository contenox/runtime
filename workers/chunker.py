from abc import ABC, abstractmethod

class Chunker(ABC):
    @abstractmethod
    def chunk(self, text: str) -> list[str]:
        raise NotImplementedError("Subclasses must implement chunk method")
