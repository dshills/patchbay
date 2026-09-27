package protocol

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestErrorContract(t *testing.T) {
	cases := map[Code]int{
		InvalidConfig: 400, InvalidRequest: 400, NotFound: 404,
		ActionNotFound: 404, ProjectNotFound: 404, PermissionDenied: 403,
		ConfirmationRequired: 409, ProviderUnavailable: 503, ExecutionFailed: 422,
		Cancelled: 409, Timeout: 504, Internal: 500, Busy: 503, ShuttingDown: 503,
	}
	for code, status := range cases {
		if !code.Valid() || code.HTTPStatus() != status {
			t.Errorf("code %s maps incorrectly", code)
		}
	}
	if Code("unknown").Valid() || Code("unknown").HTTPStatus() != http.StatusInternalServerError {
		t.Fatal("unknown errors should map to internal")
	}
	data, err := json.Marshal(ErrorResponse{Error: Error{Code: ConfirmationRequired, Message: "Explicit confirmation is required."}})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"error":{"code":"confirmation_required","message":"Explicit confirmation is required."}}` {
		t.Fatalf("wire format changed: %s", data)
	}
}

func TestInvocationAndZeroDisplayValue(t *testing.T) {
	data, err := json.Marshal(Invocation{Mode: Async, Confirmed: true, Args: map[string]any{"count": json.Number("9007199254740993")}})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"args":{"count":9007199254740993},"mode":"async","confirmed":true}` {
		t.Fatalf("numeric precision or invocation changed: %s", data)
	}
	zero := 0
	data, err = json.Marshal(DisplayResult{Progress: &zero})
	if err != nil || string(data) != `{"progress":0}` {
		t.Fatalf("zero progress omitted: %s %v", data, err)
	}
}

func TestContextPatchCanExplicitlyClearValues(t *testing.T) {
	empty := map[string]string{}
	data, err := json.Marshal(ContextPatch{Values: &empty})
	if err != nil || string(data) != `{"values":{}}` {
		t.Fatalf("explicit clear was lost: %s %v", data, err)
	}
	data, err = json.Marshal(ContextPatch{})
	if err != nil || string(data) != `{}` {
		t.Fatalf("omitted values should be unchanged: %s %v", data, err)
	}
}
