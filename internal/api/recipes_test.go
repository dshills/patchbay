package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"patchbay/internal/evidence"
	"patchbay/internal/recipe"
)

func TestRecipeUploadPrepareCommitAPI(t *testing.T) {
	f := newAPI(t)
	p, err := recipe.Inspect(context.Background(), "../../recipes/benchmark")
	if err != nil {
		t.Fatal(err)
	}
	data, err := recipe.ZIP(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	upload := func(media string) *http.Response {
		t.Helper()
		req, err := http.NewRequest("POST", "http://deckd/v1/recipes/imports", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", media)
		response, err := f.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	res := upload("application/json")
	_ = res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("wrong media accepted")
	}
	res = upload("application/zip")
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || res.StatusCode != 200 {
		t.Fatal(res.StatusCode, err, string(body))
	}
	var installed recipe.ImportResult
	if err := json.Unmarshal(body, &installed); err != nil {
		t.Fatal(err)
	}
	id := installed.Installation.ID
	request(t, f, "GET", "/v1/recipes/"+id, "", 200)
	request(t, f, "POST", "/v1/recipes/"+id+"/prepare", `{"operation":"activate","unknown":true}`, 400)
	preview := request(t, f, "POST", "/v1/recipes/"+id+"/prepare", `{"operation":"activate","mappings":{"benchmark":{"project":"p"},"demo":{"tool":"/bin/echo"}}}`, 200)
	if len(f.runtime.Jobs().List()) != 0 {
		t.Fatal("import/prepare executed content")
	}
	commit, _ := json.Marshal(recipe.Commit{Preparation: preview["id"].(string), Digest: preview["digest"].(string), RequestID: evidence.NewRequestID(time.Now()), Confirmed: true})
	one := request(t, f, "POST", "/v1/recipes/"+id+"/commit", string(commit), 200)
	two := request(t, f, "POST", "/v1/recipes/"+id+"/commit", string(commit), 200)
	if one["revision"] != two["revision"] {
		t.Fatal("duplicate request committed twice")
	}
	request(t, f, "GET", "/v1/recipes", "", 200)
}
