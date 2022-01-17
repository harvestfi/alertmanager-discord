package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Discord color values
const (
	ColorRed   = 0x992D22
	ColorGreen = 0x2ECC71
	ColorGrey  = 0x95A5A6
)

type alertManAlert struct {
	Annotations struct {
		Description string `json:"description"`
		Summary     string `json:"summary"`
		Error string `json:"error"`
		Vault string `json:"vault"`
		Network string `json:"network"`
	} `json:"annotations"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Labels       map[string]string `json:"labels"`
	StartsAt     string            `json:"startsAt"`
	Status       string            `json:"status"`
}

type alertManOut struct {
	Alerts            []alertManAlert `json:"alerts"`
	CommonAnnotations struct {
		Summary string `json:"summary"`
		Error string `json:"error"`
		Vault string `json:"vault"`
		Network string `json:"network"`
	} `json:"commonAnnotations"`
	CommonLabels struct {
		Alertname string `json:"alertname"`
	} `json:"commonLabels"`
	ExternalURL string `json:"externalURL"`
	GroupKey    string `json:"groupKey"`
	GroupLabels struct {
		Alertname string `json:"alertname"`
	} `json:"groupLabels"`
	Receiver string `json:"receiver"`
	Status   string `json:"status"`
	Version  string `json:"version"`
}

type discordOut struct {
	Content string         `json:"content"`
	Embeds  []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Color       int                 `json:"color"`
	Fields      []discordEmbedField `json:"fields"`
}

type discordEmbedField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

const defaultListenAddress = "127.0.0.1:9094"

var (
	whURL         = flag.String("webhook.url", os.Getenv("DISCORD_WEBHOOK"), "Discord WebHook URL.")
	listenAddress = flag.String("listen.address", os.Getenv("LISTEN_ADDRESS"), "Address:Port to listen on.")
)

func checkWhURL(whURL string) {
	if whURL == "" {
		log.Fatalf("Environment variable 'DISCORD_WEBHOOK' or CLI parameter 'webhook.url' not found.")
	}
	_, err := url.Parse(whURL)
	if err != nil {
		log.Fatalf("The Discord WebHook URL doesn't seem to be a valid URL.")
	}

	re := regexp.MustCompile(`https://discord(?:app)?.com/api/webhooks/[0-9]{18}/[a-zA-Z0-9_-]+`)
	if ok := re.Match([]byte(whURL)); !ok {
		log.Printf("The Discord WebHook URL doesn't seem to be valid.")
	}
}

func truncate(str string, length int) (truncated string) {
    if length <= 0 {
        return
    }
    for i, char := range str {
        if i >= length {
            break
        }
        truncated += string(char)
    }
    return
}

func sendWebhook(amo *alertManOut) {
	groupedAlerts := make(map[string][]alertManAlert)

	for _, alert := range amo.Alerts {
		groupedAlerts[alert.Status] = append(groupedAlerts[alert.Status], alert)
	}

	for status, alerts := range groupedAlerts {
		DO := discordOut{}

		RichEmbed := discordEmbed{
			Title:       fmt.Sprintf("🚨 %s", amo.CommonAnnotations.Summary),
			Description: "details:",
			Color:       ColorGrey,
			Fields:      []discordEmbedField{},
		}

		if status == "firing" {
			RichEmbed.Color = ColorRed
		} else if status == "resolved" {
			RichEmbed.Color = ColorGreen
		}

		for _, alert := range alerts {
			realname := alert.Labels["instance"]
			if strings.Contains(realname, "localhost") && alert.Labels["exported_instance"] != "" {
				realname = alert.Labels["exported_instance"]
			}

			RichEmbed.Fields = append(RichEmbed.Fields, discordEmbedField{
				Name:  fmt.Sprintf("[%s] %s", strings.ToUpper(alert.Annotations.Network), alert.Annotations.Vault),
				Value: fmt.Sprintf("```\n%s\n```", truncate(alert.Annotations.Error, 1500)),
			})
		}

		DO.Embeds = []discordEmbed{RichEmbed}

		DOD, _ := json.Marshal(DO)
		resp, err := http.Post(*whURL, "application/json", bytes.NewReader(DOD))
		if err != nil {
                       log.Fatal(err)
                }
		defer resp.Body.Close()
                body, err := ioutil.ReadAll(resp.Body)
                if err != nil {
                    log.Fatal(err)
                }
		if string(body) != "" {
                    log.Println(string(body))
	        }
	}
}

func sendRawPromAlertWarn() {
	badString := `This program is suppose to be fed by alertmanager.` + "\n" +
		`It is not a replacement for alertmanager, it is a ` + "\n" +
		`webhook target for it. Please read the README.md  ` + "\n" +
		`for guidance on how to configure it for alertmanager` + "\n" +
		`or https://prometheus.io/docs/alerting/latest/configuration/#webhook_config`

	log.Print(`/!\ -- You have misconfigured this software -- /!\`)
	log.Print(`--- --                                      -- ---`)
	log.Print(badString)

	DO := discordOut{
		Content: "",
		Embeds: []discordEmbed{
			{
				Title:       "You have misconfigured this software",
				Description: badString,
				Color:       ColorGrey,
				Fields:      []discordEmbedField{},
			},
		},
	}

	DOD, _ := json.Marshal(DO)
	http.Post(*whURL, "application/json", bytes.NewReader(DOD))
}

func main() {
	flag.Parse()
	checkWhURL(*whURL)

	if *listenAddress == "" {
		*listenAddress = defaultListenAddress
	}

	log.Printf("Listening on: %s", *listenAddress)
	log.Fatalf("Failed to listen on HTTP: %v",
		http.ListenAndServe(*listenAddress, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("%s - [%s] %s", r.Host, r.Method, r.URL.RawPath)

			b, err := ioutil.ReadAll(r.Body)
			if err != nil {
				panic(err)
			}

			amo := alertManOut{}
			err = json.Unmarshal(b, &amo)
			if err != nil {
				if isRawPromAlert(b) {
					sendRawPromAlertWarn()
					return
				}

				if len(b) > 1024 {
					log.Printf("Failed to unpack inbound alert request - %s...", string(b[:1023]))

				} else {
					log.Printf("Failed to unpack inbound alert request - %s", string(b))
				}

				return
			}

			sendWebhook(&amo)
		})))
}
