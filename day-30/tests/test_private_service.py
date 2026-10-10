import concurrent.futures
import http.client
import json
import socket
import sys
import threading
import time
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from web_server import (GenerationQueue, RateLimiter, RequestValidationError, context_bound,
                        create_server, MAX_REQUEST_BYTES)
from test_inherited import FakeOllamaServer, json_bytes


class LogicTests(unittest.TestCase):
    def test_rate_per_user_burst_and_refill(self):
        now = [0.0]
        limiter = RateLimiter(10, 3, lambda: now[0])
        self.assertEqual([limiter.check('a') for _ in range(4)], [0,0,0,6])
        self.assertEqual(limiter.check('b'), 0)
        now[0] = 6
        self.assertEqual(limiter.check('a'), 0)
        self.assertEqual(limiter.check('a'), 6)

    def test_bound_counts_all_turns_and_utf8(self):
        messages = [{'role':'system','content':'😀'}, {'role':'user','content':'Привет'},
                    {'role':'assistant','content':'你好'}, {'role':'user','content':'again'}]
        expected = len((''.join(f"<|im_start|>{m['role']}\n{m['content']}<|im_end|>\n" for m in messages)
                        + '<|im_start|>assistant\n').encode()) + 256
        self.assertEqual(context_bound(messages), expected)
        self.assertGreater(context_bound(messages), context_bound(messages[-1:]))

    def test_queue_timeout_and_release(self):
        q = GenerationQueue(1, .05)
        with q.slot():
            with concurrent.futures.ThreadPoolExecutor() as pool:
                def wait():
                    with q.slot(): pass
                with self.assertRaisesRegex(RequestValidationError, 'ожидания'):
                    pool.submit(wait).result()
        self.assertEqual(q.status()['active'], 0)
        self.assertEqual(q.status()['queued'], 0)
        with q.slot(): pass

    def test_queue_cancellation_removes_waiter(self):
        q = GenerationQueue(1, .5)
        with q.slot():
            with self.assertRaisesRegex(RequestValidationError, 'отменён'):
                with q.slot(lambda: True): pass
        self.assertEqual(q.status()['queued'], 0)


class HTTPTests(unittest.TestCase):
    def setUp(self):
        self.fake = FakeOllamaServer()
        threading.Thread(target=self.fake.serve_forever,daemon=True).start()
        self.web = create_server(port=0, proxy_secret='t'*64, allowed_origins=['https://chat.test'],
                                 rate_per_minute=60000, burst=100, queue_size=1, queue_timeout=.25,
                                 request_timeout=.25, read_timeout=1,
                                 ollama_url=f'http://127.0.0.1:{self.fake.server_address[1]}')
        threading.Thread(target=self.web.serve_forever,daemon=True).start()
        self.port = self.web.server_address[1]
        self.payload = {'messages':[{'role':'user','content':'Hi'}]}

    def tearDown(self):
        self.web.shutdown(); self.web.server_close()
        self.fake.shutdown(); self.fake.server_close()

    def request(self, path='/api/chat', payload=None, headers=None, auth=True, method='POST'):
        conn = http.client.HTTPConnection('127.0.0.1',self.port,timeout=4)
        h = {'Content-Type':'application/json'}
        if auth: h.update({'X-Private-Token':'t'*64,'X-Authenticated-User':'test'})
        h.update(headers or {})
        body = json_bytes(self.payload if payload is None else payload) if method=='POST' else None
        conn.request(method,path,body,h)
        response = conn.getresponse()
        raw = response.read(); status = response.status; hs = dict(response.getheaders()); conn.close()
        return status,hs,raw

    def test_no_proxy_and_wrong_proxy_secret(self):
        for auth,h in [(False,{}),(True,{'X-Private-Token':'wrong'})]:
            self.assertEqual(self.request(auth=auth,headers=h)[0],403)
        self.assertEqual(self.fake.chat_requests,[])

    def test_origin_curl_and_server_parameters(self):
        self.assertEqual(self.request(headers={'Origin':'https://evil.test'})[0],403)
        self.assertEqual(self.request(headers={'Origin':'null'})[0],403)
        self.assertEqual(self.request(headers={'Origin':'https://chat.test'})[0],200)
        self.assertEqual(self.request()[0],200)
        self.assertEqual(self.fake.chat_requests[-1]['options']['num_ctx'],8192)
        self.assertEqual(self.fake.chat_requests[-1]['options']['num_predict'],512)
        self.assertEqual(self.request(payload={**self.payload,'options':{'num_ctx':99999}})[0],400)
        self.assertEqual(self.request(payload={**self.payload,'model':'other'})[0],400)

    def test_context_rejected_before_rate_and_model(self):
        self.web.limiter = RateLimiter(10,1)
        self.assertEqual(self.request(payload={'systemPrompt':'','messages':[{'role':'user','content':'я'*4000}]})[0],413)
        self.assertEqual(self.fake.chat_requests,[])
        self.assertEqual(self.request()[0],200)
        status,hs,_ = self.request()
        self.assertEqual(status,429)
        self.assertIn('Retry-After',hs)
        self.assertEqual(len(self.fake.chat_requests),1)

    def test_large_body_and_slow_body(self):
        conn=http.client.HTTPConnection('127.0.0.1',self.port,timeout=3)
        conn.request('POST','/api/chat',b'x'*(MAX_REQUEST_BYTES+1),
                     {'Content-Type':'application/json','X-Private-Token':'t'*64,'X-Authenticated-User':'test'})
        self.assertEqual(conn.getresponse().status,413); conn.close()
        sock=socket.create_connection(('127.0.0.1',self.port))
        sock.sendall(('POST /api/chat HTTP/1.1\r\nHost: local\r\nContent-Length: 100\r\nContent-Type: application/json\r\nX-Private-Token: '+'t'*64+'\r\nX-Authenticated-User: test\r\n\r\n{').encode())
        sock.settimeout(2)
        self.assertIn(b'408',sock.recv(4096)); sock.close()
        self.assertEqual(self.fake.chat_requests,[])

    def test_queue_full_wait_timeout_and_recovery(self):
        self.fake.chunks=['x']*15; self.fake.chunk_delay=.04
        with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
            first=pool.submit(self.request)
            deadline=time.monotonic()+1
            while not self.web.queue.status()['active'] and time.monotonic()<deadline: time.sleep(.01)
            second=pool.submit(self.request)
            while not self.web.queue.status()['queued'] and time.monotonic()<deadline: time.sleep(.01)
            third=self.request()
            self.assertEqual(third[0],503); self.assertIn('заполнена',third[2].decode())
            self.assertEqual(second.result()[0],503)
            self.assertEqual(first.result()[0],200)
        self.assertEqual(self.web.queue.status()['active'],0)
        self.assertEqual(self.web.queue.status()['queued'],0)
        self.fake.chunk_delay=0
        self.assertEqual(self.request()[0],200)

    def test_cancel_releases_slot_and_next_request_succeeds(self):
        self.fake.chunks=['x']*100; self.fake.chunk_delay=.03
        conn=http.client.HTTPConnection('127.0.0.1',self.port,timeout=3)
        conn.request('POST','/api/chat',json_bytes(self.payload),
                     {'Content-Type':'application/json','X-Private-Token':'t'*64,'X-Authenticated-User':'test'})
        r=conn.getresponse(); self.assertEqual(r.status,200); r.readline()
        r.close(); conn.close()
        deadline=time.monotonic()+1
        while self.web.queue.status()['active'] and time.monotonic()<deadline: time.sleep(.02)
        self.assertEqual(self.web.queue.status()['active'],0)
        self.fake.chunk_delay=0
        self.assertEqual(self.request()[0],200)

    def test_absolute_generation_deadline_sends_error_after_200(self):
        self.fake.chunks=['x']*100; self.fake.chunk_delay=.025
        start=time.monotonic(); status,_,raw=self.request()
        self.assertEqual(status,200)
        events=[json.loads(l) for l in raw.splitlines()]
        self.assertEqual(events[-1]['type'],'error')
        self.assertLess(time.monotonic()-start,1.5)
        self.assertEqual(self.web.queue.status()['active'],0)

    def test_live_ready_outage_and_recovery(self):
        self.assertEqual(self.request('/health/live',method='GET')[0],200)
        self.assertEqual(self.request('/health/ready',method='GET')[0],200)
        self.fake.models=[]
        self.assertEqual(self.request('/health/live',method='GET')[0],200)
        self.assertEqual(self.request('/health/ready',method='GET')[0],503)
        self.assertEqual(self.request()[0],503)
        self.fake.models=['qwen2.5:3b']
        self.assertEqual(self.request()[0],200)

    def test_changed_template_is_not_ready_and_cannot_generate(self):
        self.fake.template='unexpected template'
        self.assertEqual(self.request('/health/ready',method='GET')[0],503)
        self.assertEqual(self.request()[0],502)
        self.assertEqual(self.fake.chat_requests,[])
        deadline=time.monotonic()+1
        while self.web.queue.status()['active'] and time.monotonic()<deadline: time.sleep(.01)
        self.assertEqual(self.web.queue.status()['active'],0)

    def test_error_and_incomplete_stream_after_http_200(self):
        for mode in ['stream_error','incomplete']:
            setattr(self.fake,mode,True)
            status,_,raw=self.request()
            self.assertEqual(status,200)
            events=[json.loads(l) for l in raw.splitlines()]
            self.assertEqual(events[-1]['type'],'error')
            self.assertEqual(self.web.queue.status()['active'],0)
            setattr(self.fake,mode,False)
        self.assertEqual(self.request()[0],200)

    def test_empty_system_is_explicitly_forwarded(self):
        self.assertEqual(self.request(payload={**self.payload,'systemPrompt':''})[0],200)
        self.assertEqual(self.fake.chat_requests[-1]['messages'][0],{'role':'system','content':''})

    def test_invalid_unicode_rejected_before_generation(self):
        payload={'messages':[{'role':'user','content':'\ud800'}]}
        conn=http.client.HTTPConnection('127.0.0.1',self.port,timeout=3)
        conn.request('POST','/api/chat',json.dumps(payload).encode(),
                     {'Content-Type':'application/json','X-Private-Token':'t'*64,'X-Authenticated-User':'test'})
        r=conn.getresponse(); self.assertEqual(r.status,400); r.read(); conn.close()
        self.assertEqual(self.fake.chat_requests,[])


if __name__=='__main__': unittest.main()
