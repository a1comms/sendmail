// Package sendmail is intended for direct sending of emails.
package sendmail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io/ioutil"
	"net/mail"
	"os"
	"os/user"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v2"
)

var (
	wg sync.WaitGroup
)

// Config of envelope
type Config struct {
	Sender     string
	Recipients []string
	Subject    string
	Body       []byte
	PortSMTP   string
}

// Envelope of message
type Envelope struct {
	*mail.Message
	Recipients []string
	PortSMTP   string
}

// NewEnvelope return new message envelope
func NewEnvelope(config *Config) (Envelope, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(config.Body))
	if err != nil {
		if len(config.Recipients) > 0 {
			msg, err = GetDumbMessage(config.Sender, config.Recipients, config.Body)
		}
		if err != nil {
			return Envelope{}, err
		}
	}

	if config.PortSMTP == "" {
		config.PortSMTP = "25"
	}

	if config.Sender != "" {
		msg.Header["From"] = []string{config.Sender}
	} else {
		sender, _ := msg.Header.AddressList("From")
		if len(sender) > 0 {
			config.Sender = sender[0].Address
		}
		if config.Sender == "" {
			user, err := user.Current()
			if err == nil {
				hostname, err := os.Hostname()
				if err == nil {
					config.Sender = user.Username + "@" + hostname
					msg.Header["From"] = []string{config.Sender}
				}
			}
		}
	}

	if config.Subject != "" {
		msg.Header["Subject"] = []string{"=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(config.Subject))}
	}

	var recipients []string

	if len(config.Recipients) > 0 {
		recipient, err := mail.ParseAddressList(strings.Join(config.Recipients, ","))
		if err == nil {
			recipients = AddressListToSlice(recipient)
		}
	} else {
		recipientsList, err := msg.Header.AddressList("To")
		if err != nil {
			return Envelope{}, err
		}
		rcpt := func(field string) []*mail.Address {
			if recipient, err := msg.Header.AddressList(field); err == nil {
				return recipient
			}
			return nil
		}
		recipientsList = append(recipientsList, rcpt("Cc")...)
		recipientsList = append(recipientsList, rcpt("Bcc")...)
		recipients = AddressListToSlice(recipientsList)
	}

	if len(recipients) == 0 {
		return Envelope{}, errors.New("no recipients listed")
	}

	return Envelope{msg, recipients, config.PortSMTP}, nil
}

func (e *Envelope) GetSender() string {
	sender, _ := e.Header.AddressList("From")

	if len(sender) > 0 {
		return sender[0].Address
	}

	return ""
}

// Send message.
// It returns channel for results of send.
// After the end of sending channel are closed.
func (e *Envelope) Send() (<-chan Result, error) {
	var relayConfig struct {
		RelayHost     string `yaml:"relay_host,omitempty"`
		RelayLogin    string `yaml:"relay_login,omitempty"`
		RelayPassword string `yaml:"relay_password,omitempty"`
	}

	data, err := ioutil.ReadFile("/etc/go-sendmail.yaml")
	if err != nil {
		return nil, fmt.Errorf("Failed to read config file: %s", err)
	}

	err = yaml.Unmarshal([]byte(data), &relayConfig)
	if err != nil {
		return nil, fmt.Errorf("Error while parsing config file: %s", err)
	}

	if relayConfig.RelayHost == "" {
		relayConfig.RelayHost = os.Getenv("SENDMAIL_SMART_HOST")
	}
	if relayConfig.RelayLogin == "" {
		relayConfig.RelayLogin = os.Getenv("SENDMAIL_SMART_LOGIN")
	}
	if relayConfig.RelayPassword == "" {
		relayConfig.RelayPassword = os.Getenv("SENDMAIL_SMART_PASSWORD")
	}

	if relayConfig.RelayHost != "" {
		return e.SendSmarthost(
			relayConfig.RelayHost,
			relayConfig.RelayLogin,
			relayConfig.RelayPassword,
		), nil
	}

	return e.SendLikeMTA(), nil
}

// GenerateMessage create body from mail.Message
func (e *Envelope) GenerateMessage() ([]byte, error) {
	if len(e.Header) == 0 {
		return nil, errors.New("empty header")
	}

	buf := bytes.NewBuffer(nil)
	keys := make([]string, 0, len(e.Header))
	for key := range e.Header {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		buf.WriteString(key + ": " + strings.Join(e.Header[key], ",") + "\r\n")
	}
	buf.WriteString("\r\n")

	_, err := buf.ReadFrom(e.Body)
	if err != nil {
		return nil, err
	}

	if !bytes.HasSuffix(buf.Bytes(), []byte("\r\n")) {
		buf.WriteString("\r\n")
	}

	return buf.Bytes(), nil
}
