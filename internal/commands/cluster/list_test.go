package cluster

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
)

func TestFetchAPIURL(t *testing.T) {
	tests := []struct {
		name     string
		response interface{}
		want     string
	}{
		{
			name: "extracts apiEndpoint from controller status",
			response: map[string]interface{}{
				"controller_statuses": []map[string]interface{}{
					{
						"data": map[string]interface{}{
							"hostedCluster": map[string]interface{}{
								"apiEndpoint": "https://api.mycluster.1fb9.0.us-east-1.rosa.openshiftapps.com:6443",
							},
						},
					},
				},
			},
			want: "https://api.mycluster.1fb9.0.us-east-1.rosa.openshiftapps.com:6443",
		},
		{
			name: "returns empty when no controller statuses",
			response: map[string]interface{}{
				"controller_statuses": []map[string]interface{}{},
			},
			want: "",
		},
		{
			name: "returns empty when data missing hostedCluster",
			response: map[string]interface{}{
				"controller_statuses": []map[string]interface{}{
					{
						"data": map[string]interface{}{
							"someOtherKey": "value",
						},
					},
				},
			},
			want: "",
		},
		{
			name: "returns empty when apiEndpoint is empty string",
			response: map[string]interface{}{
				"controller_statuses": []map[string]interface{}{
					{
						"data": map[string]interface{}{
							"hostedCluster": map[string]interface{}{
								"apiEndpoint": "",
							},
						},
					},
				},
			},
			want: "",
		},
		{
			name: "picks first non-empty apiEndpoint from multiple statuses",
			response: map[string]interface{}{
				"controller_statuses": []map[string]interface{}{
					{
						"data": map[string]interface{}{},
					},
					{
						"data": map[string]interface{}{
							"hostedCluster": map[string]interface{}{
								"apiEndpoint": "https://api.second.abcd.0.us-west-2.rosa.openshiftapps.com:6443",
							},
						},
					},
				},
			},
			want: "https://api.second.abcd.0.us-west-2.rosa.openshiftapps.com:6443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer srv.Close()

			creds := awssdk.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			}

			got := fetchAPIURL(context.Background(), srv.URL, "test-cluster-id", creds, "us-east-1")
			if got != tt.want {
				t.Errorf("fetchAPIURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFetchAPIURL_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	creds := awssdk.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}

	got := fetchAPIURL(context.Background(), srv.URL, "test-cluster-id", creds, "us-east-1")
	if got != "" {
		t.Errorf("fetchAPIURL() on server error = %q, want empty string", got)
	}
}
