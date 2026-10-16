"""The address a request came from, at /ip, and 2 MB to fetch, at /2m.bin:
what the traffic tests dial through each link. Run where the test panels are.

usage: python3 echo.py [PORT]   (18099 when not given)
"""
import http.server
import sys

BLOB = bytes(2 * 1024 * 1024)


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == '/ip':
            body = self.client_address[0].encode()
        elif self.path == '/2m.bin':
            body = BLOB
        else:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


port = int(sys.argv[1]) if len(sys.argv) > 1 else 18099
http.server.ThreadingHTTPServer(('0.0.0.0', port), Handler).serve_forever()
