#!/usr/bin/env python3
"""A Torznab indexer serving the release names given on the command line.

    python3 fake_torznab.py 18490 "Show.S00E01.1080p.WEB-GRP" ...

It answers a query the way a keyword tracker does: a release is returned when
its name holds every word of q, and everything is returned when there is no q.
Every request is logged to stderr. Links are magnets nothing seeds.
"""
import hashlib, re, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlsplit
from xml.sax.saxutils import escape

PORT, NAMES = int(sys.argv[1]), sys.argv[2:]
CAPS = """<?xml version="1.0"?><caps><server title="fake"/><limits max="100" default="100"/>
<searching><search available="yes" supportedParams="q"/>
<tv-search available="yes" supportedParams="q,season,ep"/>
<movie-search available="yes" supportedParams="q"/></searching>
<categories><category id="5000" name="TV"/></categories></caps>"""


def words(s):
    return set(re.findall(r"[a-z0-9]+", s.lower()))


def item(i, name):
    magnet = "magnet:?xt=urn:btih:%s&amp;dn=%s" % (
        hashlib.sha1(name.encode()).hexdigest(), escape(name))
    return (f"<item><title>{escape(name)}</title><guid>{i}</guid>"
            f"<pubDate>Mon, 02 Nov 2026 10:00:00 +0000</pubDate><size>{(i + 2) << 30}</size>"
            f"<category>5000</category><link>{magnet}</link>"
            f'<enclosure url="{magnet}" length="{(i + 2) << 30}" type="application/x-bittorrent"/>'
            f'<torznab:attr name="seeders" value="{40 - i}"/>'
            f'<torznab:attr name="peers" value="{45 - i}"/></item>')


class H(BaseHTTPRequestHandler):
    def do_GET(self):
        q = parse_qs(urlsplit(self.path).query)
        if q.get("t") == ["caps"]:
            body = CAPS
        else:
            want = words(q.get("q", [""])[0])
            hits = [(i, n) for i, n in enumerate(NAMES) if want <= words(n)]
            body = ('<?xml version="1.0"?><rss version="2.0" '
                    'xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>'
                    + "".join(item(i, n) for i, n in hits) + "</channel></rss>")
        self.send_response(200)
        self.send_header("Content-Type", "application/xml")
        self.end_headers()
        self.wfile.write(body.encode())

    def log_message(self, *a):
        print(re.sub(r"apikey=[^& ]*", "apikey=x", a[1]), file=sys.stderr, flush=True)


ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
