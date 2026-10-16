import argparse
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


def parse_args():
    parser = argparse.ArgumentParser(description="Simple callback receiver for tile-server demos.")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8920)
    parser.add_argument("--path", default="/callback")
    parser.add_argument("--output-dir", required=True)
    return parser.parse_args()


class State:
    def __init__(self, output_dir: Path, callback_path: str):
        self.output_dir = output_dir
        self.callback_path = callback_path
        self.counter = 0
        self.lock = threading.Lock()

    def next_file(self) -> Path:
        with self.lock:
            self.counter += 1
            name = f"{time.strftime('%Y%m%d_%H%M%S')}_{self.counter:04d}.json"
            return self.output_dir / name


def make_handler(state: State):
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path == "/health":
                self.send_response(200)
                self.send_header("Content-Type", "application/json; charset=utf-8")
                self.end_headers()
                self.wfile.write(json.dumps({"status": "ok"}).encode("utf-8"))
                return

            self.send_response(404)
            self.end_headers()

        def do_POST(self):
            self._handle_payload()

        def do_PUT(self):
            self._handle_payload()

        def _handle_payload(self):
            if self.path != state.callback_path:
                self.send_response(404)
                self.end_headers()
                return

            length = int(self.headers.get("Content-Length", "0"))
            raw = self.rfile.read(length)
            text = raw.decode("utf-8", errors="replace")
            try:
                body_json = json.loads(text)
            except json.JSONDecodeError:
                body_json = None

            record = {
                "received_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                "method": self.command,
                "path": self.path,
                "headers": {key: value for key, value in self.headers.items()},
                "body_text": text,
                "body_json": body_json,
            }

            output_path = state.next_file()
            output_path.write_text(json.dumps(record, ensure_ascii=False, indent=2), encoding="utf-8")
            print(f"[callback] saved {output_path}", flush=True)

            response = {"saved": str(output_path), "ok": True}
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.end_headers()
            self.wfile.write(json.dumps(response).encode("utf-8"))

        def log_message(self, fmt, *args):
            print("[callback] " + (fmt % args), flush=True)

    return Handler


def main():
    args = parse_args()
    output_dir = Path(args.output_dir).resolve()
    output_dir.mkdir(parents=True, exist_ok=True)

    state = State(output_dir=output_dir, callback_path=args.path)
    server = ThreadingHTTPServer((args.host, args.port), make_handler(state))
    print(f"Callback receiver listening on http://{args.host}:{args.port}{args.path}", flush=True)
    print(f"Health endpoint: http://{args.host}:{args.port}/health", flush=True)
    print(f"Output dir: {output_dir}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
