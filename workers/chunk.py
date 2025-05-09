
from workers.chunker import Chunker

class SimpleChunk(Chunker):
    def __init__(self):
        super().__init__()

    def chunk(self, text: str) -> list[str]:
        """
        Splits text into chunks of max_length.
        """
        chunks = []
        for i in range(0, len(text), 256):
            chunks.append(text[i:i+256])
        return chunks


class BetterChunk(Chunker):
    def __init__(self):
        super().__init__()

    def chunk(self, text: str) -> list[str]:
        """
        Splits text into overlapping chunks using a sliding window approach.
        """
        chunks = []
        max_window_size = 512
        step_size = 256
        adjustment_threshold = 20

        current = 0
        while current < len(text):
            end = current + max_window_size
            if end > len(text):
                end = len(text)

            chunk = text[current:end]
            last_space = chunk.rfind(' ')

            if last_space != -1 and (len(chunk) - last_space) <= adjustment_threshold:
                adjusted_end = current + last_space + 1
                chunk = text[current:adjusted_end]
                current = adjusted_end
            else:
                current += step_size

            chunks.append(chunk)

        return chunks
