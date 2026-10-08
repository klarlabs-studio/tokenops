package accounts

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

// awsCredentials are AWS signing credentials: an access key and its
// secret, with a session token for temporary ones.
type awsCredentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
}

// signV4 signs req with AWS Signature Version 4 for service in region
// (docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html):
// every header already on the request is signed, with Host and X-Amz-Date.
// The canonical query and the HMAC are Volcengine's (doubao.go), which
// follows the same scheme.
// body is the request body, hashed into the signature.
func signV4(req *http.Request, body []byte, c awsCredentials, region, service string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	if c.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", c.SessionToken)
	}
	headers := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = strings.Join(v, ",")
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, k := range names {
		canonHeaders.WriteString(k + ":" + strings.TrimSpace(headers[k]) + "\n")
	}
	signed := strings.Join(names, ";")
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{
		req.Method, path, volcengineQuery(req.URL.Query()), canonHeaders.String(), signed, sha256Hex(body),
	}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	key := hmacSHA256([]byte("AWS4"+c.SecretAccessKey), day)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, service)
	key = hmacSHA256(key, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.AccessKeyID+"/"+scope+
		", SignedHeaders="+signed+", Signature="+sig)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
