"""Local OpenAI fixture. It never makes outbound requests or logs credentials."""
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import time


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        if self.path != '/v1/chat/completions':
            self.send_error(404)
            return
        length = int(self.headers.get('Content-Length', '0'))
        if length < 1 or length > 1048576:
            self.send_error(400)
            return
        request = json.loads(self.rfile.read(length))
        response = {'id': 'chatcmpl-local', 'object': 'chat.completion',
                    'created': int(time.time()), 'model': request['model'],
                    'choices': [{'index': 0, 'message': {'role': 'assistant',
                                'content': 'local fixture'}, 'finish_reason': 'stop'}],
                    'usage': {'prompt_tokens': 1000, 'completion_tokens': 1000,
                              'total_tokens': 2000}}
        body = json.dumps(response).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == '__main__':
    HTTPServer(('0.0.0.0', 4001), Handler).serve_forever()
