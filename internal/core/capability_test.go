package core

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestEnsureCACreatesNewKeypair(t *testing.T) {
	dir := t.TempDir()
	ca, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	if ca == nil {
		t.Fatal("EnsureCA returned nil")
	}
	if len(ca.PrivateKey) != ed25519.PrivateKeySize {
		t.Fatalf("private key size = %d, want %d", len(ca.PrivateKey), ed25519.PrivateKeySize)
	}
	if len(ca.PublicKey) != ed25519.PublicKeySize {
		t.Fatalf("public key size = %d, want %d", len(ca.PublicKey), ed25519.PublicKeySize)
	}

	if _, err := os.Stat(filepath.Join(dir, "ca.key")); err != nil {
		t.Fatalf("ca.key not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ca.pub")); err != nil {
		t.Fatalf("ca.pub not created: %v", err)
	}
}

func TestEnsureCALoadsExistingKeypair(t *testing.T) {
	dir := t.TempDir()
	ca1, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA (first): %v", err)
	}
	ca2, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA (second): %v", err)
	}
	if string(ca2.PrivateKey) != string(ca1.PrivateKey) {
		t.Fatal("second load produced different private key")
	}
	if string(ca2.PublicKey) != string(ca1.PublicKey) {
		t.Fatal("second load produced different public key")
	}
}

func TestEnsureCAConcurrent(t *testing.T) {
	dir := t.TempDir()
	const goroutines = 20
	results := make([]*CAKeyPair, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			ca, err := EnsureCA(dir)
			if err != nil {
				t.Errorf("goroutine %d: EnsureCA: %v", idx, err)
				return
			}
			results[idx] = ca
		}(i)
	}
	wg.Wait()

	first := results[0]
	if first == nil {
		t.Fatal("first goroutine returned nil")
	}
	for i := 1; i < goroutines; i++ {
		if results[i] == nil {
			t.Fatalf("goroutine %d returned nil", i)
		}
		if string(results[i].PrivateKey) != string(first.PrivateKey) {
			t.Fatalf("goroutine %d: private key differs from first", i)
		}
		if string(results[i].PublicKey) != string(first.PublicKey) {
			t.Fatalf("goroutine %d: public key differs from first", i)
		}
	}
}

func TestIssueTokenRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ca, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	agentPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate agent key: %v", err)
	}

	subject := TokenSubject{
		JobID:   "job_01ABC",
		WorkID:  "work_01XYZ",
		Role:    "worker",
		Adapter: "claude",
		Model:   "claude-sonnet-4-6",
	}
	caps := []string{CapWorkUpdate, CapWorkNoteAdd}

	token, err := IssueToken(ca, agentPub, subject, caps, 30*time.Minute)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if token.Version != 1 {
		t.Fatalf("version = %d, want 1", token.Version)
	}
	if token.Expired() {
		t.Fatal("freshly issued token should not be expired")
	}
	if !token.HasCapability(CapWorkUpdate) {
		t.Fatal("token should have work:update")
	}
	if token.HasCapability(CapWorkAttest) {
		t.Fatal("worker token should not have work:attest")
	}

	signable := token.Signable()
	if signable.Subject.JobID != "job_01ABC" {
		t.Fatalf("signable subject job_id = %q", signable.Subject.JobID)
	}

	payload, err := json.Marshal(signable)
	if err != nil {
		t.Fatalf("marshal signable: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(token.Signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if !ed25519.Verify(ca.PublicKey, payload, sig) {
		t.Fatal("signature verification failed")
	}
}

func TestIssueTokenExpired(t *testing.T) {
	dir := t.TempDir()
	ca, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}

	agentPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate agent key: %v", err)
	}

	subject := TokenSubject{JobID: "j", WorkID: "w", Role: "worker"}
	token, err := IssueToken(ca, agentPub, subject, []string{CapWorkUpdate}, 0)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if token.Expired() {
		t.Fatal("freshly issued token should not be expired")
	}

	expired := *token
	expired.ExpiresAt = time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	if !expired.Expired() {
		t.Fatal("token with past ExpiresAt should be expired")
	}
}

func TestWriteCredentialCreatesFile(t *testing.T) {
	dir := t.TempDir()
	ca, err := EnsureCA(dir)
	if err != nil {
		t.Fatalf("EnsureCA: %v", err)
	}
	agentPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate agent key: %v", err)
	}

	subject := TokenSubject{JobID: "j", WorkID: "w", Role: "worker"}
	token, err := IssueToken(ca, agentPub, subject, []string{CapWorkUpdate}, 0)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}

	cred := AgentCredential{
		Token:      *token,
		PrivateKey: base64.StdEncoding.EncodeToString([]byte("fake-key")),
	}
	path, err := WriteCredential(dir, cred)
	if err != nil {
		t.Fatalf("WriteCredential: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read credential file: %v", err)
	}
	if string(data) == "" {
		t.Fatal("credential file is empty")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("credential file does not exist: %v", err)
	}
}

func TestExpiredParsesRFC3339(t *testing.T) {
	token := CapabilityToken{
		Version:   1,
		ExpiresAt: time.Now().UTC().Add(-time.Second).Format(time.RFC3339),
	}
	if !token.Expired() {
		t.Fatal("token expired 1 second ago should be expired")
	}

	token2 := CapabilityToken{
		Version:   1,
		ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}
	if token2.Expired() {
		t.Fatal("token expiring in 1 hour should not be expired")
	}
}

func TestCapabilitiesForRole(t *testing.T) {
	caps := CapabilitiesForRole("worker")
	if len(caps) != 2 {
		t.Fatalf("worker caps = %v, want 2", caps)
	}

	caps = CapabilitiesForRole("unknown_role")
	if caps != nil {
		t.Fatalf("unknown role caps = %v, want nil", caps)
	}
}

func TestAttestationSignable(t *testing.T) {
	now := time.Now().UTC()
	rec := AttestationRecord{
		AttestationID: "att_001",
		SubjectKind:   "work",
		SubjectID:     "work_01ABC",
		Result:        "pass",
		Summary:       "all checks green",
		JobID:         "job_01XYZ",
		Method:        "automated",
		CreatedBy:     "agent-worker",
		CreatedAt:     now,
	}

	sig := rec.Signable()

	if sig.AttestationID != rec.AttestationID {
		t.Fatalf("AttestationID = %q, want %q", sig.AttestationID, rec.AttestationID)
	}
	if sig.SubjectKind != rec.SubjectKind {
		t.Fatalf("SubjectKind = %q, want %q", sig.SubjectKind, rec.SubjectKind)
	}
	if sig.SubjectID != rec.SubjectID {
		t.Fatalf("SubjectID = %q, want %q", sig.SubjectID, rec.SubjectID)
	}
	if sig.Result != rec.Result {
		t.Fatalf("Result = %q, want %q", sig.Result, rec.Result)
	}
	if sig.Summary != rec.Summary {
		t.Fatalf("Summary = %q, want %q", sig.Summary, rec.Summary)
	}
	if sig.JobID != rec.JobID {
		t.Fatalf("JobID = %q, want %q", sig.JobID, rec.JobID)
	}
	if sig.Method != rec.Method {
		t.Fatalf("Method = %q, want %q", sig.Method, rec.Method)
	}
	if sig.CreatedBy != rec.CreatedBy {
		t.Fatalf("CreatedBy = %q, want %q", sig.CreatedBy, rec.CreatedBy)
	}

	// CreatedAt must be RFC3339 formatted.
	parsed, err := time.Parse(time.RFC3339, sig.CreatedAt)
	if err != nil {
		t.Fatalf("CreatedAt %q is not valid RFC3339: %v", sig.CreatedAt, err)
	}
	if !parsed.Equal(now.Truncate(time.Second)) {
		t.Fatalf("CreatedAt parsed = %v, want %v", parsed, now.Truncate(time.Second))
	}
}

func TestSignAndVerifyAttestationRecord(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	rec := AttestationRecord{
		AttestationID: "att_002",
		SubjectKind:   "work",
		SubjectID:     "work_02DEF",
		Result:        "fail",
		Summary:       "lint errors found",
		CreatedAt:     time.Now().UTC(),
	}

	signable := rec.Signable()
	sig, err := SignJSON(signable, priv)
	if err != nil {
		t.Fatalf("SignJSON: %v", err)
	}
	if sig == "" {
		t.Fatal("SignJSON returned empty signature")
	}

	if !VerifyJSONSignature(signable, sig, pub) {
		t.Fatal("VerifyJSONSignature failed for valid signature")
	}

	// Tamper with signable and verify it fails.
	tampered := signable
	tampered.Result = "pass"
	if VerifyJSONSignature(tampered, sig, pub) {
		t.Fatal("VerifyJSONSignature should fail for tampered payload")
	}

	// Wrong key should also fail.
	otherPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}
	if VerifyJSONSignature(signable, sig, otherPub) {
		t.Fatal("VerifyJSONSignature should fail for wrong public key")
	}
}
