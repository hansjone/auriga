import http.client
import importlib.util
import json
from pathlib import Path
import threading
import unittest


spec = importlib.util.spec_from_file_location('local_upstream', Path(__file__).with_name('fake-upstream.py'))
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)


class FakeUpstreamTest(unittest.TestCase):
    def setUp(self):
        self.server = fixture.HTTPServer(('127.0.0.1', 0), fixture.Handler)
        self.thread = threading.Thread(target=self.server.serve_forever)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()

    def request(self, stream):
        connection = http.client.HTTPConnection(*self.server.server_address, timeout=5)
        connection.request('POST', '/v1/chat/completions', json.dumps({'model': 'fixture', 'messages': [], 'stream': stream}))
        response = connection.getresponse()
        result = response.status, response.getheader('Content-Type'), response.read()
        connection.close()
        return result

    def test_json_contains_usage(self):
        status, content_type, body = self.request(False)
        self.assertEqual(status, 200)
        self.assertEqual(content_type, 'application/json')
        self.assertEqual(json.loads(body)['usage']['total_tokens'], 2000)

    def test_stream_is_openai_sse_with_usage_and_done(self):
        status, content_type, body = self.request(True)
        self.assertEqual(status, 200)
        self.assertEqual(content_type, 'text/event-stream')
        frames = [line[6:] for line in body.decode().splitlines() if line.startswith('data: ')]
        self.assertEqual(frames[-1], '[DONE]')
        events = [json.loads(frame) for frame in frames[:-1]]
        self.assertTrue(any(event.get('choices') and event['choices'][0]['delta'].get('content') for event in events))
        self.assertEqual(events[-1]['usage']['total_tokens'], 2000)

    def test_completion_ids_are_unique_for_usage_logs(self):
        first = json.loads(self.request(False)[2])['id']
        second = json.loads(self.request(False)[2])['id']
        self.assertNotEqual(first, second)


if __name__ == '__main__':
    unittest.main()
