#!/usr/bin/env python3
"""FTP or FTPS test server for the backup tests (user fwtest / fwtest).

    python3 tests/ftpserver.py ROOT PORT            plain FTP
    python3 tests/ftpserver.py ROOT PORT CERT.pem   FTPS (explicit AUTH TLS)
Needs pyftpdlib (and pyopenssl for FTPS): pip install pyftpdlib pyopenssl
"""
import os
import sys

from pyftpdlib.authorizers import DummyAuthorizer
from pyftpdlib.handlers import FTPHandler, TLS_FTPHandler
from pyftpdlib.servers import FTPServer

root, port = sys.argv[1], int(sys.argv[2])
os.makedirs(root, exist_ok=True)
auth = DummyAuthorizer()
auth.add_user("fwtest", "fwtest", root, perm="elradfmwMT")
if len(sys.argv) > 3:
    handler = TLS_FTPHandler
    handler.certfile = sys.argv[3]
    handler.tls_control_required = True
    handler.tls_data_required = True
else:
    handler = FTPHandler
handler.authorizer = auth
FTPServer(("127.0.0.1", port), handler).serve_forever()
