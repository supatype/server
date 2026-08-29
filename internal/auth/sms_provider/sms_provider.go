package sms_provider

import (
	"fmt"
	"time"

	"github.com/supatype/server/internal/conf"
)

// overrides the SmsProvider set to always return the mock provider
var MockProvider SmsProvider = nil

var defaultTimeout time.Duration = time.Second * 10

const SMSProvider = "sms"
const WhatsappProvider = "whatsapp"

// SetHTTPTimeout sets the bound on outbound SMS provider calls. A non-positive
// duration leaves the default in place.
//
// This was a package init reading the environment directly; see
// internal/auth/provider for the same change.
func SetHTTPTimeout(d time.Duration) {
	if d > 0 {
		defaultTimeout = d
	}
}

type SmsProvider interface {
	SendMessage(phone, message, channel, otp string) (string, error)
	VerifyOTP(phone, token string) error
}

func GetSmsProvider(config conf.GlobalConfiguration) (SmsProvider, error) {
	if MockProvider != nil {
		return MockProvider, nil
	}

	switch name := config.Sms.Provider; name {
	case "twilio":
		return NewTwilioProvider(config.Sms.Twilio)
	case "messagebird":
		return NewMessagebirdProvider(config.Sms.Messagebird)
	case "textlocal":
		return NewTextlocalProvider(config.Sms.Textlocal)
	case "vonage":
		return NewVonageProvider(config.Sms.Vonage)
	case "twilio_verify":
		return NewTwilioVerifyProvider(config.Sms.TwilioVerify)
	default:
		return nil, fmt.Errorf("sms Provider %s could not be found", name)
	}
}

func IsValidMessageChannel(channel string, config *conf.GlobalConfiguration) bool {
	if config.Hook.SendSMS.Enabled {
		// channel doesn't matter if SMS hook is enabled
		return true
	}
	switch channel {
	case SMSProvider:
		return true
	case WhatsappProvider:
		return config.Sms.Provider == "twilio" || config.Sms.Provider == "twilio_verify"
	default:
		return false
	}
}
