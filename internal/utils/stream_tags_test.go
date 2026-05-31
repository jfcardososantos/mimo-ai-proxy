package utils

import "testing"

func TestThinkingTagLengthsUsedInParser(t *testing.T) {
	sample := ThinkingOpenTag + "plan" + ThinkingCloseTag + "execute"
	afterOpen := sample[len(ThinkingOpenTag):]
	if afterOpen != "plan"+ThinkingCloseTag+"execute" {
		t.Fatalf("unexpected slice after open tag: %q", afterOpen)
	}
	closeIdx := len("plan")
	afterClose := afterOpen[closeIdx+len(ThinkingCloseTag):]
	if afterClose != "execute" {
		t.Fatalf("unexpected slice after close tag: %q", afterClose)
	}
}
