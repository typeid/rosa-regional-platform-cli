package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/openshift-online/rosa-regional-platform-cli/internal/aws"
	"github.com/openshift-online/rosa-regional-platform-cli/internal/config"
	"github.com/spf13/cobra"
)

type listOptions struct {
	limit  int
	offset int
	status string
	output string
}

type clusterSpec struct {
	Placement string `json:"placement"`
	Version   string `json:"version"`
	CloudURL  string `json:"cloudUrl"`
}

type condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type clusterStatus struct {
	Conditions []condition `json:"conditions"`
}

type clusterItem struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	CreatedAt string        `json:"created_at"`
	Spec      clusterSpec   `json:"spec"`
	Status    clusterStatus `json:"status"`
}

type listResponse struct {
	Items  []clusterItem `json:"items"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

func newListCommand() *cobra.Command {
	opts := &listOptions{
		limit:  50,
		offset: 0,
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List clusters from the platform API",
		Long: `List clusters from the platform API.

This command queries the platform API to retrieve a list of clusters.

Example:
  rosactl cluster list
  rosactl cluster list --limit 10
  rosactl cluster list --status Ready`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd.Context(), opts)
		},
	}

	cmd.Flags().IntVar(&opts.limit, "limit", opts.limit, "Maximum number of clusters to return (1-100)")
	cmd.Flags().IntVar(&opts.offset, "offset", opts.offset, "Number of clusters to skip")
	cmd.Flags().StringVar(&opts.status, "status", opts.status, "Filter by status (Pending, Progressing, Ready, Failed)")
	cmd.Flags().StringVarP(&opts.output, "output", "o", "table", "Output format: table or json")

	return cmd
}

func runList(ctx context.Context, opts *listOptions) error {
	baseURL, err := config.GetPlatformAPIURL()
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/api/v0/clusters?limit=%d&offset=%d", baseURL, opts.limit, opts.offset)
	if opts.status != "" {
		endpoint = fmt.Sprintf("%s&status=%s", endpoint, opts.status)
	}

	cfg, err := aws.NewConfig(ctx)
	if err != nil {
		return fmt.Errorf("failed to load AWS config: %w", err)
	}

	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve AWS credentials: %w", err)
	}

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	body, err := signedGet(ctx, endpoint, creds, region)
	if err != nil {
		return err
	}

	if opts.output == "json" {
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			fmt.Println(string(body))
			return nil
		}
		prettyJSON, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(prettyJSON))
		return nil
	}

	var result listResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	apiURLs := make([]string, len(result.Items))
	for i, cluster := range result.Items {
		apiURLs[i] = fetchAPIURL(ctx, baseURL, cluster.ID, creds, region)
	}

	return displayTable(result.Items, apiURLs)
}

func displayTable(clusters []clusterItem, apiURLs []string) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	if _, err := fmt.Fprintln(w, "ID\tNAME\tAPI URL\tVERSION\tAVAILABLE\tREADY\tMESSAGE"); err != nil {
		return err
	}

	for i, cluster := range clusters {
		available := getConditionStatus(cluster.Status.Conditions, "Available")
		ready := getConditionStatus(cluster.Status.Conditions, "Ready")
		message := getConditionMessage(cluster.Status.Conditions, "Ready")

		apiURL := "-"
		if i < len(apiURLs) && apiURLs[i] != "" {
			apiURL = apiURLs[i]
		}

		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			cluster.ID,
			cluster.Name,
			apiURL,
			cluster.Spec.Version,
			available,
			ready,
			message,
		); err != nil {
			return err
		}
	}

	return w.Flush()
}

func getConditionStatus(conditions []condition, condType string) string {
	for _, cond := range conditions {
		if cond.Type == condType {
			return cond.Status
		}
	}
	return "-"
}

func getConditionMessage(conditions []condition, condType string) string {
	for _, cond := range conditions {
		if cond.Type == condType && cond.Message != "" {
			return cond.Message
		}
	}
	for _, cond := range conditions {
		if cond.Type == "Adapter1Successful" && cond.Message != "" {
			return cond.Message
		}
	}
	for _, cond := range conditions {
		if cond.Message != "" {
			return cond.Message
		}
	}
	return ""
}

func signedGet(ctx context.Context, url string, creds awssdk.Credentials, region string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	const emptyBodyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	signer := v4.NewSigner()
	if err := signer.SignHTTP(ctx, creds, req, emptyBodyHash, "execute-api", region, time.Now()); err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func fetchAPIURL(ctx context.Context, baseURL, clusterID string, creds awssdk.Credentials, region string) string {
	endpoint := fmt.Sprintf("%s/api/v0/clusters/%s/statuses", baseURL, clusterID)
	body, err := signedGet(ctx, endpoint, creds, region)
	if err != nil {
		return ""
	}

	var envelope struct {
		ControllerStatuses []struct {
			Data map[string]interface{} `json:"data"`
		} `json:"controller_statuses"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return ""
	}

	for _, cs := range envelope.ControllerStatuses {
		if hc, ok := cs.Data["hostedCluster"].(map[string]interface{}); ok {
			if ep, ok := hc["apiEndpoint"].(string); ok && ep != "" {
				return ep
			}
		}
	}
	return ""
}
