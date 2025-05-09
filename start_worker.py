import os
from workers.chunk import BetterChunk
from workers.worker import cycle
from workers.plaintext import TextParser
from workers.worker import load_config

def main():
    print("loading configuration from environment variables.")
    config = load_config()
    print("configuration loaded successfully.")
    worker_type = os.getenv("WORKER_TYPE", "plaintext").lower()
    if worker_type == "plaintext":
        print("starting plaintext worker")
        cycle(TextParser(), BetterChunk(), config)
        print("plaintext worker finished")
    else:
        raise ValueError(f"Unknown worker type: {worker_type}")

if __name__ == "__main__":
    main()
