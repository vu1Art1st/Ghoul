package agent

import "testing"

func TestEvaluateFindingEvidence(t *testing.T) {
	replay := "curl -s https://target/api?id=1\nHTTP/1.1 200 OK\nset-cookie: session=admin\n" +
		"response body contains private account 42 and the requested marker; repeated twice with identical result"
	grade, correction := EvaluateFindingEvidence("SQLi", "high", replay)
	if correction != "" || grade != EvidenceReplay {
		t.Fatalf("replay evidence: grade=%s correction=%q", grade, correction)
	}
	if _, correction := EvaluateFindingEvidence("known", "high", "Server: nginx/1.18.0"); correction == "" {
		t.Fatal("version-only evidence should be rejected for high severity")
	}
	if _, correction := EvaluateFindingEvidence("SQLi", "critical", "HTTP/1.1 200 OK\nshort"); correction == "" {
		t.Fatal("short critical evidence should be rejected")
	}
	if _, correction := EvaluateFindingEvidence("SQLi", "medium", "observed response difference"); correction != "" {
		t.Fatal("medium evidence without mechanical proof should remain reportable")
	}
}

func TestEvaluateCredentialEvidenceRequiresSessionProof(t *testing.T) {
	if _, correction := EvaluateFindingEvidence("默认口令", "high", "HTTP/1.1 200 OK\nlogin page returned"); correction == "" {
		t.Fatal("credential finding without session proof should be rejected")
	}
	proof := "HTTP/1.1 200 OK\nset-cookie: session=admin\nlogin successful\n" +
		"the authenticated dashboard returned the admin profile, a valid CSRF token and private user records; " +
		"the same session was replayed twice and reached the protected administration endpoint both times"
	grade, correction := EvaluateFindingEvidence("默认口令", "high", proof)
	if correction != "" || grade == EvidenceUnverified {
		t.Fatalf("credential proof should pass: grade=%s correction=%q", grade, correction)
	}
}
