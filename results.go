package aviato

import (
	"net/url"
)

// Result is what an action returns: build one with [Success], [Error], [HTML], [File],
// [Redirect] or [Webhook] (or the matching [ActionContext] methods).
type Result interface {
	isResult()
}

// SuccessResult reports a successful action.
type SuccessResult struct {
	Message string
	// Invalidated lists relations of the target record the dashboard should refresh.
	Invalidated []string
}

// ErrorResult reports a business error (a failed validation, a refused operation).
type ErrorResult struct {
	Message string
	// HTML is an optional rich explanation, sanitized by the dashboard.
	HTML string
}

// HTMLResult shows HTML to the user (sanitized by the dashboard).
type HTMLResult struct {
	HTML string
}

// FileResult is downloaded by the user. The SDK keeps the content for five minutes and serves
// it once, to the agent, from GET <basePath>/files/{ref}.
type FileResult struct {
	Name     string
	MimeType string
	Content  []byte
}

// RedirectResult sends the user to an internal dashboard path or an absolute URL.
type RedirectResult struct {
	Path string
	URL  string
}

// WebhookResult is an HTTP request performed by the user's browser (never by AI agents).
type WebhookResult struct {
	URL     string
	Method  string
	Headers map[string]string
	Body    string
}

func (SuccessResult) isResult()  {}
func (ErrorResult) isResult()    {}
func (HTMLResult) isResult()     {}
func (FileResult) isResult()     {}
func (RedirectResult) isResult() {}
func (WebhookResult) isResult()  {}

// Success reports a successful action, optionally invalidating relations of the target record.
func Success(message string, invalidated ...string) SuccessResult {
	return SuccessResult{Message: message, Invalidated: invalidated}
}

// Error reports a business error to the user.
func Error(message string) ErrorResult {
	return ErrorResult{Message: message}
}

// HTML shows HTML to the user.
func HTML(html string) HTMLResult {
	return HTMLResult{HTML: html}
}

// File makes the user download content.
func File(name, mimeType string, content []byte) FileResult {
	return FileResult{Name: name, MimeType: mimeType, Content: content}
}

// Redirect sends the user to target: an absolute URL (with a scheme) or an internal dashboard
// path.
func Redirect(target string) RedirectResult {
	if parsed, err := url.Parse(target); err == nil && parsed.Scheme != "" {
		return RedirectResult{URL: target}
	}
	return RedirectResult{Path: target}
}

// Webhook makes the user's browser perform an HTTP request. The method defaults to POST.
func Webhook(url, method string, headers map[string]string, body string) WebhookResult {
	if method == "" {
		method = "POST"
	}
	return WebhookResult{URL: url, Method: method, Headers: headers, Body: body}
}
