// Package sesclient implements mailer.Client using AWS SES v2.
package sesclient

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// Client sends transactional email via AWS SES v2.
// Credentials are loaded from the standard AWS provider chain
// (env vars, ~/.aws/credentials, EC2/ECS instance role).
type Client struct {
	ses  *sesv2.Client
	from string
}

// New returns a Client configured with the ambient AWS credentials.
// fromAddress must be a verified SES identity.
// region defaults to us-east-1 if AWS_REGION / AWS_DEFAULT_REGION is unset.
func New(ctx context.Context, fromAddress string) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
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
