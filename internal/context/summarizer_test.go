package contextmanager

import (
	"context"
	"testing"

	"MyCode/internal/llm"
)

func TestLLMSummarizerDisablesToolsAndCollectsText(t *testing.T) {
	client := &summarizerTestClient{}
	summarizer := LLMSummarizer{Client: client}
	response, err := summarizer.Summarize(context.Background(), SummarizeRequest{
		PreviousSummary: "old", Messages: []StoredMessage{{ID: "m1", Content: "new"}}, TokenBudget: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != "compact summary" {
		t.Fatalf("content = %q", response.Content)
	}
	if client.request == nil || len(client.request.Tools) != 0 {
		t.Fatalf("summarizer tools = %#v", client.request.Tools)
	}
}

type summarizerTestClient struct {
	request *llm.StreamRequest
}

func (c *summarizerTestClient) Stream(request *llm.StreamRequest) (<-chan llm.StreamEvent, <-chan error) {
	c.request = request
	events := make(chan llm.StreamEvent, 2)
	errs := make(chan error)
	events <- llm.TextStream{Text: "compact "}
	events <- llm.TextStream{Text: "summary"}
	close(events)
	close(errs)
	return events, errs
}
