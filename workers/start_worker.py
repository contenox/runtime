import os
from workers.worker import cycle
from workers.plaintext import TextParser
from workers.worker import load_config

def main():
    print("loading configuration from environment variables.")
    config = load_config()
    print("configuration loaded successfully.")
    worker_type = os.getenv("WORKER_TYPE", "plaintext").lower()
    if worker_type == "plaintext":
        cycle(TextParser(),config)
    else:
        raise ValueError(f"Unknown worker type: {worker_type}")

if __name__ == "__main__":
    main()
