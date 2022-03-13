package mail

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"strings"
)

// MaxHeaderLength defines the maximum line length for a mail header
// RFC 2047 suggests 76 characters
const MaxHeaderLength = 76

// msgWriter handles the I/O to the io.WriteCloser of the SMTP client
type msgWriter struct {
	d   int8
	err error
	mpw [3]*multipart.Writer
	n   int64
	pw  io.Writer
	w   io.Writer
}

// Write implements the io.Writer interface for msgWriter
func (mw *msgWriter) Write(p []byte) (int, error) {
	if mw.err != nil {
		return 0, fmt.Errorf("failed to write due to previous error: %w", mw.err)
	}

	var n int
	n, mw.err = mw.w.Write(p)
	mw.n += int64(n)
	return n, mw.err
}

// writeMsg formats the message and sends it to its io.Writer
func (mw *msgWriter) writeMsg(m *Msg) {
	if _, ok := m.genHeader[HeaderDate]; !ok {
		m.SetDate()
	}
	for k, v := range m.genHeader {
		mw.writeHeader(k, v...)
	}
	for _, t := range []AddrHeader{HeaderFrom, HeaderTo, HeaderCc} {
		if al, ok := m.addrHeader[t]; ok {
			var v []string
			for _, a := range al {
				v = append(v, a.String())
			}
			mw.writeHeader(Header(t), v...)
		}
	}
	mw.writeHeader(HeaderMIMEVersion, string(m.mimever))

	if m.hasAlt() {
		mw.startMP(MIMEAlternative, m.boundary)
		mw.writeString("\r\n\r\n")
	}
	for _, p := range m.parts {
		mw.writePart(p, m.charset)
	}
	if m.hasAlt() {
		mw.stopMP()
	}
}

// startMP writes a multipart beginning
func (mw *msgWriter) startMP(mt MIMEType, b string) {
	mp := multipart.NewWriter(mw)
	if b != "" {
		mw.err = mp.SetBoundary(b)
	}

	ct := fmt.Sprintf("multipart/%s;\r\n boundary=%s", mt, mp.Boundary())
	mw.mpw[mw.d] = mp

	if mw.d == 0 {
		mw.writeString(fmt.Sprintf("%s: %s", HeaderContentType, ct))
	}
	if mw.d > 0 {
		mw.newPart(map[string][]string{"Content-Type": {ct}})
	}
	mw.d++
}

// stopMP closes the multipart
func (mw *msgWriter) stopMP() {
	if mw.d > 0 {
		mw.err = mw.mpw[mw.d-1].Close()
		mw.d--
	}
}

// newPart creates a new MIME multipart io.Writer and sets the partwriter to it
func (mw *msgWriter) newPart(h map[string][]string) {
	mw.pw, mw.err = mw.mpw[mw.d-1].CreatePart(h)
}

// writePart writes the corresponding part to the Msg body
func (mw *msgWriter) writePart(p *Part, cs Charset) {
	ct := fmt.Sprintf("%s; charset=%s", p.ctype, cs)
	cte := p.enc.String()
	if mw.d == 0 {
		mw.writeHeader(HeaderContentType, ct)
		mw.writeHeader(HeaderContentTransferEnc, cte)
		mw.writeString("\r\n")
	}
	if mw.d > 0 {
		mh := textproto.MIMEHeader{}
		mh.Add(string(HeaderContentType), ct)
		mh.Add(string(HeaderContentTransferEnc), cte)
		mw.newPart(mh)
	}
	mw.writeBody(p.w, p.enc)
}

// writeString writes a string into the msgWriter's io.Writer interface
func (mw *msgWriter) writeString(s string) {
	if mw.err != nil {
		return
	}
	var n int
	n, mw.err = io.WriteString(mw.w, s)
	mw.n += int64(n)
}

// writeHeader writes a header into the msgWriter's io.Writer
func (mw *msgWriter) writeHeader(k Header, v ...string) {
	mw.writeString(string(k))
	if len(v) == 0 {
		mw.writeString(":\r\n")
		return
	}
	mw.writeString(": ")

	// Chars left: MaxHeaderLength - "<Headername>: " - "CRLF"
	cl := MaxHeaderLength - len(k) - 4
	for i, s := range v {
		nfl := 0
		if i < len(v) {
			nfl = len(v[i])
		}
		if cl-len(s) < 1 {
			if p := strings.IndexByte(s, ' '); p != -1 {
				mw.writeString(s[:p])
				mw.writeString("\r\n ")
				mw.writeString(s[p:])
				cl -= len(s[p:])
				continue
			}
		}
		if cl < 1 || cl-nfl < 1 {
			mw.writeString("\r\n")
			cl = MaxHeaderLength - 4
			if i != len(v) {
				mw.writeString(" ")
				cl -= 1
			}
		}
		mw.writeString(s)
		cl -= len(s)

		if i != len(v)-1 {
			mw.writeString(", ")
			cl -= 2
		}

	}
	mw.writeString("\r\n")
}

// writeBody writes an io.Reader into an io.Writer using provided Encoding
func (mw *msgWriter) writeBody(f func(io.Writer) error, e Encoding) {
	var w io.Writer
	var ew io.WriteCloser
	if mw.d == 0 {
		w = mw.w
	}
	if mw.d > 0 {
		w = mw.pw
	}

	switch e {
	case EncodingQP:
		ew = quotedprintable.NewWriter(w)
	case EncodingB64:
		ew = base64.NewEncoder(base64.StdEncoding, w)
	case NoEncoding:
		mw.err = f(w)
		return
	default:
		ew = quotedprintable.NewWriter(w)
	}

	mw.err = f(ew)
	mw.err = ew.Close()
}
