package exampleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/roigada/payment-gateway/internal/app"
)

const defaultTimeout = 5 * time.Second

type Client struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client
}

func NewClient(baseURL string, token string, httpClient *http.Client) (*Client, error) {
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	if parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" {
		return nil, fmt.Errorf("example api base url must be absolute")
	}

	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	return &Client{
		baseURL:    parsedBaseURL,
		token:      token,
		httpClient: httpClient,
	}, nil
}

func (c *Client) NotifyTaskCreated(ctx context.Context, task app.TaskResult) error {
	return c.postTask(ctx, "/task-created", task)
}

func (c *Client) NotifyTaskCompleted(ctx context.Context, task app.TaskResult) error {
	return c.postTask(ctx, "/task-completed", task)
}

type taskPayload struct {
	TaskID    string `json:"task_id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

func (c *Client) postTask(ctx context.Context, path string, task app.TaskResult) error {
	payload := taskPayload{
		TaskID:    task.ID,
		Title:     task.Title,
		Completed: task.Completed,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	endpoint := c.baseURL.JoinPath(path)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("example api request failed: status %d", response.StatusCode)
	}

	return nil
}
