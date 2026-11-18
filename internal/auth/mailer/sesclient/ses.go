// Package sesclient implements mailer.Client using AWS SES v2.
package sesclient

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// Client sends transactional email via AWS SES v2.
type Client struct {
	ses  *sesv2.Client
	from string
}

// Credentials are a project's own SES access key and region. Left empty, the standard AWS provider
// chain applies (env vars, ~/.aws/credentials, an instance role), which is what a self-hosted stack
// on AWS uses. A hosted project has no ambient credentials of its own and no process environment it
// controls, so it names them here, through the server's configuration, which is reloaded in place.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	Region          string
}

// options turns c into AWS config options: a static provider only when both halves of the key are
// set (half a key is no key, so the provider chain still applies), and the region when one is named.
func (c Credentials) options() []func(*config.LoadOptions) error {
	var opts []func(*config.LoadOptions) error
	if c.AccessKeyID != "" && c.SecretAccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, "")))
	}
	if c.Region != "" {
		opts = append(opts, config.WithRegion(c.Region))
	}
	return opts
}

// New returns a Client sending from fromAddress, which must be a verified SES identity, with creds
// when they are set and the ambient AWS credentials otherwise.
func New(ctx context.Context, fromAddress string, creds Credentials) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, creds.options()...)
	if err != nil {
		return nil, fmt.Errorf("ses: load config: %w", err)
	}
	return &Client{
		ses:  sesv2.NewFromConfig(cfg),
		from: fromAddress,
	}, nil
}

// Mail implements mailer.Client. The body is sent as the HTML part.
// headers and typ are accepted for interface compatibility but not forwarded.
func (c *Client) Mail(
	ctx context.Context,
	to string,
	subject string,
	body string,
	headers map[string][]string,
	typ string,
) error {
	input := &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(c.from),
		Destination: &types.Destination{
			ToAddresses: []string{to},
		},
		Content: &types.EmailContent{
			Simple: &types.Message{
				Subject: &types.Content{
					Data:    aws.String(subject),
					Charset: aws.String("UTF-8"),
				},
				Body: &types.Body{
					Html: &types.Content{
						Data:    aws.String(body),
						Charset: aws.String("UTF-8"),
					},
				},
			},
		},
	}

	if _, err := c.ses.SendEmail(ctx, input); err != nil {
		return fmt.Errorf("ses: SendEmail: %w", err)
	}
	return nil
}
