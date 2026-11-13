package db

import (
	"encoding/json"
	"testing"
)

func TestNormalizePocRecordDefaultsAndValidation(t *testing.T) {
	record := &PocRecord{Name: "example"}
	if err := normalizePocRecord(record); err != nil {
		t.Fatal(err)
	}
	if record.ScopeType != "component" || record.ValidationStatus != "draft" || record.Confidence != "observed" {
		t.Fatalf("unexpected defaults: %+v", record)
	}
	if string(record.Fingerprint) != "{}" || string(record.Metadata) != "{}" {
		t.Fatalf("unexpected JSON defaults: fingerprint=%s metadata=%s", record.Fingerprint, record.Metadata)
	}
	if record.CVEIDs == nil || record.CWEIDs == nil || record.AssetIDs == nil {
		t.Fatalf("array fields must default to empty arrays: %+v", record)
	}
}

func TestNormalizePocRecordRejectsInvalidEnums(t *testing.T) {
	for _, record := range []*PocRecord{
		{Name: "bad scope", ScopeType: "host"},
		{Name: "bad status", ValidationStatus: "confirmed"},
	} {
		if err := normalizePocRecord(record); err == nil {
			t.Fatalf("record accepted: %+v", record)
		}
	}
}

func TestNormalizePocRecordPreservesCTFMetadata(t *testing.T) {
	record := &PocRecord{
		Name:      "ctf challenge",
		ScopeType: "ctf",
		Metadata:  json.RawMessage(`{"flag":"flag{demo}","poa":["recon","exploit"]}`),
	}
	if err := normalizePocRecord(record); err != nil {
		t.Fatal(err)
	}
	if string(record.Metadata) != `{"flag":"flag{demo}","poa":["recon","exploit"]}` {
		t.Fatalf("metadata changed: %s", record.Metadata)
	}
}
