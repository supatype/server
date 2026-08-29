package security

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fmt"

	"github.com/pkg/errors"
	"github.com/supatype/server/internal/utilities"
)

type AuthRequest struct {
	// Security carries the captcha token a client attaches to an auth request.
	//
	// The JSON name is part of the request body, so its spelling ships with the
	// client SDK that sends it.
	Security MetaSecurity `json:"supatype_meta_security"`
}

type MetaSecurity struct {
	Token string `json:"captcha_token"`
}

type VerificationResponse struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
	Hostname   string   `json:"hostname"`
}

// Client calls the captcha provider.
//
// Its timeout used to be read from the environment in a package init, which
// parsed the value before configuration existed and killed the process with
// log.Fatalf if it was malformed. It is set from configuration at startup now.
var Client = &http.Client{Timeout: 10 * time.Second}

// SetHTTPTimeout sets the bound on the captcha provider call. A non-positive
// duration leaves the default in place.
func SetHTTPTimeout(d time.Duration) {
	if d > 0 {
		Client = &http.Client{Timeout: d}
	}
}

func VerifyRequest(requestBody *AuthRequest, clientIP, secretKey, captchaProvider string) (VerificationResponse, error) {
	captchaResponse := strings.TrimSpace(requestBody.Security.Token)

	if captchaResponse == "" {
		return VerificationResponse{}, errors.New("no captcha response (captcha_token) found in request")
	}

	captchaURL, err := GetCaptchaURL(captchaProvider)
	if err != nil {
		return VerificationResponse{}, err
	}

	return verifyCaptchaCode(captchaResponse, secretKey, clientIP, captchaURL)
}

func verifyCaptchaCode(token, secretKey, clientIP, captchaURL string) (VerificationResponse, error) {
	data := url.Values{}
	data.Set("secret", secretKey)
	data.Set("response", token)
	data.Set("remoteip", clientIP)
	// TODO (darora): pipe through sitekey

	r, err := http.NewRequest("POST", captchaURL, strings.NewReader(data.Encode()))
	if err != nil {
		return VerificationResponse{}, errors.Wrap(err, "couldn't initialize request object for captcha check")
	}
	r.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Add("Content-Length", strconv.Itoa(len(data.Encode())))
	res, err := Client.Do(r)
	if err != nil {
		return VerificationResponse{}, errors.Wrap(err, "failed to verify captcha response")
	}
	defer utilities.SafeClose(res.Body)

	var verificationResponse VerificationResponse

	if err := json.NewDecoder(res.Body).Decode(&verificationResponse); err != nil {
		return VerificationResponse{}, errors.Wrap(err, "failed to decode captcha response: not JSON")
	}

	return verificationResponse, nil
}

func GetCaptchaURL(captchaProvider string) (string, error) {
	switch captchaProvider {
	case "hcaptcha":
		return "https://hcaptcha.com/siteverify", nil
	case "turnstile":
		return "https://challenges.cloudflare.com/turnstile/v0/siteverify", nil
	default:
		return "", fmt.Errorf("captcha Provider %q could not be found", captchaProvider)
	}
}
