package ld

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPostCodeReferenceRepository(t *testing.T) {
	specs := []struct {
		name           string
		responseStatus int
		expectedErr    error
	}{
		{"succeeds", 200, nil},
		{"succeeds on conflict", 409, nil},
		{"fails on internal error", 500, RepositoryPostErr},
	}
	for _, tt := range specs {
		t.Run(tt.name, func(t *testing.T) {
			testServer := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
				res.WriteHeader(tt.responseStatus)
			}))
			defer testServer.Close()

			client := InitApiClient(ApiOptions{ApiKey: "api-x", ProjKey: "default", BaseUri: testServer.URL})
			err := client.PostCodeReferenceRepository(RepoParams{Type: "custom", Name: "test"})
			require.Equal(t, tt.expectedErr, err)
		})
	}
}

func TestPutCodeReferenceBranch(t *testing.T) {
	specs := []struct {
		name           string
		responseStatus int
		expectedErr    error
	}{
		{"succeeds", 200, nil},
		{"fails on internal error", 500, BranchPutErr},
	}

	for _, tt := range specs {
		t.Run(tt.name, func(t *testing.T) {
			testServer := httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
				res.WriteHeader(tt.responseStatus)
			}))
			defer testServer.Close()

			client := InitApiClient(ApiOptions{ApiKey: "api-x", ProjKey: "default", BaseUri: testServer.URL})
			err := client.PutCodeReferenceBranch(BranchRep{}, RepoParams{Type: "custom", Name: "test"})
			require.Equal(t, tt.expectedErr, err)
		})
	}
}
