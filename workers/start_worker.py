import os
from workers.worker import cycle
from workers.plaintext import TextParser

def main():
    worker_type = os.getenv("WORKER_TYPE", "plaintext").lower()
    if worker_type == "plaintext":
        cycle(TextParser())
    else:
        raise ValueError(f"Unknown worker type: {worker_type}")

if __name__ == "__main__":
    main()
