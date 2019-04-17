/*
Copyright 2019 IBM
Copyright 2019 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

/* This sample is based on the event-display tool
from knative/eventing-sources */

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/cloudevents/sdk-go/pkg/cloudevents"
	"github.com/cloudevents/sdk-go/pkg/cloudevents/transport/http"
	"github.com/knative/eventing-sources/pkg/kncloudevents"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
)

/*
This tool receives a cloud event from the github source in
knative/eventing-sources, it parses metadata out of it, and it
uses it to:
- provision Tekton PipelineResources that match the metadata
- trigger a PipelineRun that uses those resources

Exmple of the input cloud event:

☁  CloudEvent: valid ✅
Context Attributes,
  SpecVersion: 0.2
  Type: dev.knative.eventing.samples.heartbeat
  Source: https://github.com/knative/eventing-sources/cmd/heartbeats/#local/demo
  ID: 3d2b5a1f-10ca-437b-a374-9c49e43c02fb
  Time: 2019-03-14T21:21:29.366002Z
  ContentType: application/json
  Extensions:
    the: 42
    beats: true
    heart: yes
Transport Context,
  URI: /
  Host: localhost:8080
  Method: POST
Data,
  {"id":162,"label":""}
*/

func process(ctx context.Context, event cloudevents.Event) {
	// Display the event in any case
	// display(ctx, event)
	fmt.Println("Now let's do some extra processing")

	// Do something if the event is a push
	switch event.SpecVersion() {

	case cloudevents.CloudEventsVersionV02:
		fmt.Println("I've got a V2 Cloud event")

		if ec, ok := event.Context.(cloudevents.EventContextV02); ok {
			switch ec.Type {
			case "dev.knative.source.github.push":
				fmt.Println("I've got a push")

				// Parse the JSON data
				var f interface{}
				err := json.Unmarshal(event.Data.([]byte), &f)
				if err != nil {
				    log.Fatal(err)
				}
				eventData := f.(map[string]interface{})
				repo, ok := eventData["repository"].(map[string]interface{})
				if ok == false {
					log.Fatal("Unexpected data") }
				repoURL, ok := repo["html_url"].(string)
				if ok == false {
					log.Fatal("Unexpected data")
				}
				gitResource := tektonv1.PipelineResource{
					ObjectMeta: metav1.ObjectMeta{
						Name: "git-resource",
						Namespace: "dev",
					},
					Spec: tektonv1.PipelineResourceSpec{
						Type: tektonv1.PipelineResourceTypeGit,
						Params: []tektonv1.Param{
							{
								Name: repoURL,
								Value: "https://git-repo",
							},
						},
					},
				}
				fmt.Println(gitResource)

			default:
				fmt.Println("Ignore non-push event")
			}
		}

	default:
		fmt.Println("Ignore non-V2 events")
	}
}

func display(ctx context.Context, event cloudevents.Event) {

	valid := event.Validate()

	b := strings.Builder{}
	b.WriteString("\n")
	b.WriteString("☁️  CloudEvent: ")

	if valid == nil {
		b.WriteString("valid ✅\n")
	} else {
		b.WriteString("invalid ❌\n")
	}
	if valid != nil {
		b.WriteString(fmt.Sprintf("Validation Error: %s\n", valid.Error()))
	}

	b.WriteString("Context Attributes,\n")

	var extensions map[string]interface{}

	switch event.SpecVersion() {
	case cloudevents.CloudEventsVersionV01:
		if ec, ok := event.Context.(cloudevents.EventContextV01); ok {
			b.WriteString("  CloudEventsVersion: " + ec.CloudEventsVersion + "\n")
			b.WriteString("  EventType: " + ec.EventType + "\n")
			if ec.EventTypeVersion != nil {
				b.WriteString("  EventTypeVersion: " + *ec.EventTypeVersion + "\n")
			}
			b.WriteString("  Source: " + ec.Source.String() + "\n")
			b.WriteString("  EventID: " + ec.EventID + "\n")
			if ec.EventTime != nil {
				b.WriteString("  EventTime: " + ec.EventTime.String() + "\n")
			}
			if ec.SchemaURL != nil {
				b.WriteString("  SchemaURL: " + ec.SchemaURL.String() + "\n")
			}
			if ec.ContentType != nil {
				b.WriteString("  ContentType: " + *ec.ContentType + "\n")
			}
			extensions = ec.Extensions
		}

	case cloudevents.CloudEventsVersionV02:
		if ec, ok := event.Context.(cloudevents.EventContextV02); ok {
			b.WriteString("  SpecVersion: " + ec.SpecVersion + "\n")
			b.WriteString("  Type: " + ec.Type + "\n")
			b.WriteString("  Source: " + ec.Source.String() + "\n")
			b.WriteString("  ID: " + ec.ID + "\n")
			if ec.Time != nil {
				b.WriteString("  Time: " + ec.Time.String() + "\n")
			}
			if ec.SchemaURL != nil {
				b.WriteString("  SchemaURL: " + ec.SchemaURL.String() + "\n")
			}
			if ec.ContentType != nil {
				b.WriteString("  ContentType: " + *ec.ContentType + "\n")
			}
			extensions = ec.Extensions
		}

	case cloudevents.CloudEventsVersionV03:
		if ec, ok := event.Context.(cloudevents.EventContextV03); ok {
			b.WriteString("  SpecVersion: " + ec.SpecVersion + "\n")
			b.WriteString("  Type: " + ec.Type + "\n")
			b.WriteString("  Source: " + ec.Source.String() + "\n")
			b.WriteString("  ID: " + ec.ID + "\n")
			if ec.Time != nil {
				b.WriteString("  Time: " + ec.Time.String() + "\n")
			}
			if ec.SchemaURL != nil {
				b.WriteString("  SchemaURL: " + ec.SchemaURL.String() + "\n")
			}
			if ec.DataContentType != nil {
				b.WriteString("  DataContentType: " + *ec.DataContentType + "\n")
			}
			extensions = ec.Extensions
		}
	default:
		b.WriteString(event.String() + "\n")
	}

	if extensions != nil && len(extensions) > 0 {
		b.WriteString("  Extensions: \n")
		for k, v := range extensions {
			b.WriteString(fmt.Sprintf("    %s: %v\n", k, v))
		}
	}

	tx := http.TransportContextFrom(ctx)
	b.WriteString("Transport Context,\n")
	b.WriteString("  URI: " + tx.URI + "\n")
	b.WriteString("  Host: " + tx.Host + "\n")
	b.WriteString("  Method: " + tx.Method + "\n")

	b.WriteString("Data,\n  ")
	if strings.HasPrefix(event.DataContentType(), "application/json") {
		var prettyJSON bytes.Buffer
		error := json.Indent(&prettyJSON, event.Data.([]byte), "  ", "  ")
		if error != nil {
			b.Write(event.Data.([]byte))
		} else {
			b.Write(prettyJSON.Bytes())
		}
	} else {
		b.Write(event.Data.([]byte))
	}
	b.WriteString("\n")

	fmt.Print(b.String())
}

func main() {
	c, err := kncloudevents.NewDefaultClient()
	if err != nil {
		log.Fatal("Failed to create client, ", err)
	}

	log.Fatal(c.StartReceiver(context.Background(), process))
}
