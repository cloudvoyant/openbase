package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/cloudvoyant/openbase/libs/testutils/liveenv"
)

// Public API -----------------------------------------------------------------

// RequireLiveKindeUser verifies either AgentMail OTP credentials (AGENTMAIL_API_KEY + E2E_P1_EMAIL) or KINDE_E2E_USERNAME/PASSWORD.
func RequireLiveKindeUser(t *testing.T) (string, string) {
	t.Helper()
	return liveenv.RequireKindeUser(t)
}

// Private helpers ------------------------------------------------------------

var otpSixDigitsRe = regexp.MustCompile(`\b(\d{6})\b`)

type browserAuthReport struct {
	LoginLandingURL string `json:"loginLandingUrl"`
	Authenticated   bool   `json:"authenticated"`
	SignedInEmail   string `json:"signedInEmail"`
	LoggedOutAgain  bool   `json:"loggedOutAgain"`
}

type pageProbeState struct {
	Found bool   `json:"found"`
	Loc   string `json:"loc"`
	Text  string `json:"text"`
}

type agentMailMessageSummary struct {
	MessageID string   `json:"message_id"`
	Timestamp string   `json:"timestamp"`
	To        []string `json:"to"`
}

func verifyKindeBrowserLogin(t *testing.T, h *LiveStackHarness, baseURL, bypassSecret string) browserAuthReport {
	t.Helper()
	email, password := RequireLiveKindeUser(t)
	agentMailKey := os.Getenv("AGENTMAIL_API_KEY")
	h.RegisterSecret(password, agentMailKey)

	ctx, cleanupBrowser := newHeadlessChromeCtx(t, 120*time.Second)
	defer cleanupBrowser()

	primeVercelBypassCookie(t, ctx, baseURL, bypassSecret)
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/api/auth/login")); err != nil {
		t.Fatalf("navigate to /api/auth/login: %v", err)
	}
	requestedAt := completeKindeEmailStep(t, ctx, email)
	completeKindeSecondFactor(t, ctx, email, password, agentMailKey, requestedAt)
	landingURL := waitForAppCallbackLanding(t, ctx, baseURL)
	health := readBrowserHealthJSON(t, ctx, baseURL)
	loggedOut := verifyBrowserLogout(t, ctx, baseURL)

	if !health.Auth.Authenticated || !strings.EqualFold(health.Auth.User.Email, email) {
		t.Fatalf("expected authenticated session for %q, got %+v", email, health.Auth)
	}
	if !loggedOut {
		t.Fatalf("expected /api/auth/logout to clear session and return to %s/", baseURL)
	}
	return browserAuthReport{
		LoginLandingURL: landingURL,
		Authenticated:   health.Auth.Authenticated,
		SignedInEmail:   health.Auth.User.Email,
		LoggedOutAgain:  loggedOut,
	}
}

func newHeadlessChromeCtx(t *testing.T, timeout time.Duration) (context.Context, func()) {
	t.Helper()
	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.NoSandbox,
	)
	timeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), timeout)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(timeoutCtx, opts...)
	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	return tabCtx, func() {
		cancelTab()
		cancelAlloc()
		cancelTimeout()
	}
}

func primeVercelBypassCookie(t *testing.T, ctx context.Context, baseURL, bypassSecret string) {
	t.Helper()
	if bypassSecret == "" {
		return
	}
	bypassURL := fmt.Sprintf(
		"%s/?x-vercel-protection-bypass=%s&x-vercel-set-bypass-cookie=samesitenone",
		baseURL,
		url.QueryEscape(bypassSecret),
	)
	if err := chromedp.Do(ctx, chromedp.Navigate(bypassURL)); err != nil {
		t.Fatalf("prime Vercel bypass cookie: %v", err)
	}
	waitForPageText(t, ctx, 30*time.Second, "Sign in with Kinde OIDC")
}

func completeKindeEmailStep(t *testing.T, ctx context.Context, email string) time.Time {
	t.Helper()
	const emailSel = `input[type="email"], input[name*="email" i], input[autocomplete="email"]`
	waitForSelectorOrCallbackErr(t, ctx, 45*time.Second, emailSel)
	requestedAt := time.Now().UTC()
	err := chromedp.Do(
		ctx,
		chromedp.WaitVisible(emailSel),
		chromedp.Click(emailSel),
		chromedp.SendKeys(emailSel, email),
		chromedp.Click(`button[type="submit"]`),
	)
	if err != nil {
		t.Fatalf("submit Kinde email step: %v", err)
	}
	return requestedAt
}

func completeKindeSecondFactor(
	t *testing.T,
	ctx context.Context,
	email, password, agentMailKey string,
	requestedAt time.Time,
) {
	t.Helper()
	const otpOrPwSel = `input[name="p_confirmation_code"], input[type="password"]`
	waitForSelectorOrCallbackErr(t, ctx, 45*time.Second, otpOrPwSel)
	hasOTP, _ := chromedp.Run(ctx, chromedp.Evaluate[bool](`Boolean(document.querySelector('input[name="p_confirmation_code"]'))`))
	if hasOTP {
		if agentMailKey == "" {
			t.Fatalf("Kinde requested Email + code OTP for %s, require AGENTMAIL_API_KEY", email)
		}
		otp := waitForAgentMailOTP(t, agentMailKey, email, requestedAt)
		submitKindeInput(t, ctx, `input[name="p_confirmation_code"]`, otp)
		return
	}
	submitKindeInput(t, ctx, `input[type="password"]`, password)
}

func submitKindeInput(t *testing.T, ctx context.Context, selector, value string) {
	t.Helper()
	err := chromedp.Do(
		ctx,
		chromedp.WaitVisible(selector),
		chromedp.Click(selector),
		chromedp.SendKeys(selector, value),
		chromedp.Click(`button[type="submit"]`),
	)
	if err != nil {
		t.Fatalf("submit Kinde input %s: %v", selector, err)
	}
}

func waitForAgentMailOTP(t *testing.T, apiKey, email string, requestedAt time.Time) string {
	t.Helper()
	inboxID := resolveAgentMailInboxID(t, apiKey, email)
	afterStr := requestedAt.Add(-5 * time.Second).Format(time.RFC3339)
	deadline := time.Now().Add(75 * time.Second)
	for time.Now().Before(deadline) {
		if code := pollAgentMailMessages(t, apiKey, inboxID, email, afterStr); code != "" {
			return code
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out waiting for Kinde 6-digit OTP at AgentMail inbox %s", email)
	return ""
}

func resolveAgentMailInboxID(t *testing.T, apiKey, email string) string {
	t.Helper()
	raw := agentMailGET(t, apiKey, "https://api.agentmail.to/v0/inboxes?limit=100")
	var page struct {
		Inboxes []struct {
			InboxID string `json:"inbox_id"`
			Email   string `json:"email"`
		} `json:"inboxes"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("unmarshal AgentMail inboxes: %v", err)
	}
	for _, inb := range page.Inboxes {
		if strings.EqualFold(inb.Email, email) {
			return inb.InboxID
		}
	}
	t.Fatalf("AgentMail inbox not found for %s", email)
	return ""
}

func pollAgentMailMessages(t *testing.T, apiKey, inboxID, email, afterRFC3339 string) string {
	t.Helper()
	u := fmt.Sprintf(
		"https://api.agentmail.to/v0/inboxes/%s/messages?limit=20&after=%s",
		url.PathEscape(inboxID),
		url.QueryEscape(afterRFC3339),
	)
	var list struct {
		Messages []agentMailMessageSummary `json:"messages"`
	}
	if json.Unmarshal(agentMailGET(t, apiKey, u), &list) != nil {
		return ""
	}
	slices.SortFunc(list.Messages, func(a, b agentMailMessageSummary) int {
		return strings.Compare(b.Timestamp, a.Timestamp)
	})
	for _, msg := range list.Messages {
		if !addressedToEmail(msg.To, email) {
			continue
		}
		if code := extractSingleOTPFromMessage(t, apiKey, inboxID, msg.MessageID); code != "" {
			return code
		}
	}
	return ""
}

func extractSingleOTPFromMessage(t *testing.T, apiKey, inboxID, msgID string) string {
	t.Helper()
	u := fmt.Sprintf("https://api.agentmail.to/v0/inboxes/%s/messages/%s", url.PathEscape(inboxID), url.PathEscape(msgID))
	var detail struct {
		ExtractedText string `json:"extracted_text"`
		Text          string `json:"text"`
		ExtractedHTML string `json:"extracted_html"`
		HTML          string `json:"html"`
	}
	if json.Unmarshal(agentMailGET(t, apiKey, u), &detail) != nil {
		return ""
	}
	body := firstNonEmpty(detail.ExtractedText, detail.Text, detail.ExtractedHTML, detail.HTML)
	matches := otpSixDigitsRe.FindAllStringSubmatch(body, -1)
	var codes []string
	for _, m := range matches {
		if !slices.Contains(codes, m[1]) {
			codes = append(codes, m[1])
		}
	}
	if len(codes) == 1 {
		return codes[0]
	}
	return ""
}

func addressedToEmail(recipients []string, email string) bool {
	lower := strings.ToLower(email)
	for _, r := range recipients {
		if strings.Contains(strings.ToLower(r), lower) {
			return true
		}
	}
	return false
}

func agentMailGET(t *testing.T, apiKey, targetURL string) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("AgentMail GET %s: %v", targetURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("AgentMail GET %s status=%d: %s", targetURL, resp.StatusCode, string(raw))
	}
	return raw
}

func waitForSelectorOrCallbackErr(t *testing.T, ctx context.Context, maxWait time.Duration, cssSel string) {
	t.Helper()
	deadline := time.Now().Add(maxWait)
	js := fmt.Sprintf(`(() => ({
		found: Boolean(document.querySelector(%q)),
		loc: window.location.href,
		text: (document.body && document.body.innerText) ? document.body.innerText.slice(0, 600) : ""
	}))()`, cssSel)
	var last pageProbeState
	for time.Now().Before(deadline) {
		state, err := chromedp.Run(ctx, chromedp.Evaluate[pageProbeState](js))
		if err == nil {
			last = state
			if strings.Contains(state.Text, "Invalid callback URL") {
				t.Fatalf("Kinde rejected callback URL at %s:\n%s", state.Loc, state.Text)
			}
			if state.Found {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s at %s; body:\n%s", cssSel, last.Loc, last.Text)
}

func waitForAppCallbackLanding(t *testing.T, ctx context.Context, baseURL string) string {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var last pageProbeState
	for time.Now().Before(deadline) {
		state, err := readPageProbeState(ctx)
		if err == nil {
			last = state
			if strings.HasPrefix(state.Loc, baseURL) && !strings.Contains(state.Loc, "/api/auth/") && strings.Contains(state.Text, "Signed in as") {
				return state.Loc
			}
			if strings.Contains(state.Loc, "/api/auth/kinde_callback") && strings.Contains(state.Text, "Token exchange failed") {
				t.Fatalf("callback token exchange failed at %s: %s", state.Loc, state.Text)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for signed-in dashboard at %s (current=%s body=%q)", baseURL, last.Loc, last.Text)
	return ""
}

func readBrowserHealthJSON(t *testing.T, ctx context.Context, baseURL string) fullStackHealthResponse {
	t.Helper()
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/api/health")); err != nil {
		t.Fatalf("navigate to /api/health in browser: %v", err)
	}
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.body ? document.body.innerText : ""`))
	if err != nil {
		t.Fatalf("read /api/health in browser: %v", err)
	}
	var out fullStackHealthResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		t.Fatalf("unmarshal browser /api/health (%q): %v", raw, err)
	}
	return out
}

func verifyBrowserLogout(t *testing.T, ctx context.Context, baseURL string) bool {
	t.Helper()
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL+"/api/auth/logout")); err != nil {
		t.Fatalf("navigate to /api/auth/logout: %v", err)
	}
	waitForPageText(t, ctx, 30*time.Second, "Sign in with Kinde OIDC")
	loc, _ := chromedp.Run(ctx, chromedp.Location())
	return strings.HasPrefix(loc, baseURL)
}

func waitForPageText(t *testing.T, ctx context.Context, maxWait time.Duration, needle string) {
	t.Helper()
	deadline := time.Now().Add(maxWait)
	var last pageProbeState
	for time.Now().Before(deadline) {
		if state, err := readPageProbeState(ctx); err == nil {
			last = state
			if strings.Contains(state.Text, needle) {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q at %s (body=%q)", needle, last.Loc, last.Text)
}

func readPageProbeState(ctx context.Context) (pageProbeState, error) {
	const js = `(() => ({
		found: false,
		loc: window.location.href,
		text: (document.body && document.body.innerText) ? document.body.innerText.slice(0, 600) : ""
	}))()`
	return chromedp.Run(ctx, chromedp.Evaluate[pageProbeState](js))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
