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

/*
This example is based on the event-display tool from knative/eventing-sources
*/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cloudevents/sdk-go/pkg/cloudevents"
	"github.com/cloudevents/sdk-go/pkg/cloudevents/transport/http"
	"github.com/knative/eventing-sources/pkg/kncloudevents"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	restclient "k8s.io/client-go/rest"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	tektonv1client "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"

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

/*
The constants here are a shortcut to config options.
Eventually it would be nice to have these in a configmap or so.
*/

const (
	targetNamespace = "default"
	targetServiceAccount = "default"
	imageBaseURL = "us.icr.io/andreaf"
	testImageName = "health-test-node:latest"
	apiPipelineName = "dev-test-build-api"
	fePipelineName = "dev-test-build-frontend"
)

func process(ctx context.Context, event cloudevents.Event) {
	// Display the event in any case
	display(ctx, event)

	// Setup an in-cluster k8s client.
	// I shouldn't really setup a client for every event... but there it is.
	cfg, err := restclient.InClusterConfig()
	if err != nil {
		log.Fatal("Failed to create a k8s client, ", err)
	}
	clientset, err := tektonv1client.NewForConfig(cfg)
  if err != nil {
      log.Fatal(err)
	}
	// Check if the pipelines are defined
	pipelines, err := clientset.TektonV1alpha1().Pipelines(targetNamespace).List(metav1.ListOptions{})
	if err != nil {
		fmt.Println(err)
	}
	pipelinesMap := make(map[string]tektonv1.Pipeline)
	for _, pipeline := range pipelines.Items {
		pipelinesMap[pipeline.Name] = pipeline
	}
	if _, ok := pipelinesMap[apiPipelineName]; ok == false {
    log.Fatal("API Pipeline not found")
	}
	if _, ok := pipelinesMap[fePipelineName]; ok == false {
    log.Fatal("FE Pipeline not found")
	}
	// Do something if the event is a push
	switch event.SpecVersion() {

	case cloudevents.CloudEventsVersionV02:
		if ec, ok := event.Context.(cloudevents.EventContextV02); ok {
			switch ec.Type {
			case "dev.knative.source.github.push":
				// Only do something on GitHub push events
				fmt.Println("Processing a GitHub push")

				// Parse the JSON data
				var f interface{}
				err := json.Unmarshal(event.Data.([]byte), &f)
				if err != nil {
				    log.Fatal(err)
				}
				eventData := f.(map[string]interface{})

				// Extract the repo URL
				repo, ok := eventData["repository"].(map[string]interface{})
				if ok == false {
					log.Fatal("Unexpected data") }
				repoURL, ok := repo["html_url"].(string)
				if ok == false {
					log.Fatal("Unexpected data")
				}

				// Extract the git sha
				headCommit, ok := eventData["head_commit"].(map[string]interface{})
				if ok == false {
					log.Fatal("Unexpected data") }
				gitSHA, ok := headCommit["id"].(string)
				if ok == false {
					log.Fatal("Unexpected data")
				}

				// Build the image tag
				var imageTag = gitSHA[0:6]

				// Build the git resource
				var gitResourceName strings.Builder
				fmt.Fprintf(&gitResourceName, "git-resource-%s", imageTag)

				gitResource := tektonv1.PipelineResource{
					ObjectMeta: metav1.ObjectMeta{
						Name: gitResourceName.String(),
						Namespace: targetNamespace,
						Labels: map[string]string{
							"app": "health",
							"tag": imageTag,
						},
					},
					Spec: tektonv1.PipelineResourceSpec{
						Type: tektonv1.PipelineResourceTypeGit,
						Params: []tektonv1.Param{
							tektonv1.Param {
								Name: "url",
								Value: repoURL,
							},
							tektonv1.Param {
								Name: "revision",
								Value: gitSHA,
							},
						},
					},
				}

				// Build the API image resource
				var imageResourceAPIName strings.Builder
				fmt.Fprintf(&imageResourceAPIName, "image-resource-api-%s", imageTag)
				var imageAPIURL strings.Builder
				fmt.Fprintf(&imageAPIURL, "%s/health-api", imageBaseURL)

				imageResourceAPI := tektonv1.PipelineResource{
					ObjectMeta: metav1.ObjectMeta{
						Name: imageResourceAPIName.String(),
						Namespace: targetNamespace,
						Labels: map[string]string{
							"app": "health",
							"tag": imageTag,
						},
					},
					Spec: tektonv1.PipelineResourceSpec{
						Type: tektonv1.PipelineResourceTypeImage,
						Params: []tektonv1.Param{
							tektonv1.Param {
								Name: "url",
								Value: imageAPIURL.String(),
							},
						},
					},
				}

				// Build the Frontend image resource
				var imageResourceFEName strings.Builder
				fmt.Fprintf(&imageResourceFEName, "image-resource-frontend-%s", imageTag)
				var imageFEURL strings.Builder
				fmt.Fprintf(&imageFEURL, "%s/health-frontend", imageBaseURL)

				imageResourceFE := tektonv1.PipelineResource{
					ObjectMeta: metav1.ObjectMeta{
						Name: imageResourceFEName.String(),
						Namespace: targetNamespace,
						Labels: map[string]string{
							"app": "health",
							"tag": imageTag,
						},
					},
					Spec: tektonv1.PipelineResourceSpec{
						Type: tektonv1.PipelineResourceTypeImage,
						Params: []tektonv1.Param{
							tektonv1.Param {
								Name: "url",
								Value: imageFEURL.String(),
							},
						},
					},
				}

				// Build the API PipelineRun
				var pipelineRunAPIName strings.Builder
				fmt.Fprintf(&pipelineRunAPIName, "pipeline-run-api-%s", imageTag)

				pipelineRunAPI := tektonv1.PipelineRun{
					ObjectMeta: metav1.ObjectMeta{
						Name: pipelineRunAPIName.String(),
						Namespace: targetNamespace,
						Labels: map[string]string{
							"app": "health",
							"component": "api",
							"tag": imageTag,
						},
					},
					Spec: tektonv1.PipelineRunSpec{
						PipelineRef: tektonv1.PipelineRef{Name: apiPipelineName},
						Trigger: tektonv1.PipelineTrigger{
							Type: tektonv1.PipelineTriggerTypeManual},
						ServiceAccount: targetServiceAccount,
						Params: []tektonv1.Param{{
							Name:  "imageTag", Value: imageTag,
						},},
						Timeout: &metav1.Duration{Duration: 1 * time.Hour},
						Resources: []tektonv1.PipelineResourceBinding{
							{
								Name: "src",
								ResourceRef: tektonv1.PipelineResourceRef{
									Name: gitResourceName.String(),
								},
							},
							{
								Name: "builtImage",
								ResourceRef: tektonv1.PipelineResourceRef{
									Name: imageResourceAPIName.String(),
								},
							},
						},
					},
				}

				// Build the FE PipelineRun
				var pipelineRunFEName strings.Builder
				fmt.Fprintf(&pipelineRunFEName, "pipeline-run-frontend-%s", imageTag)
				var testImageNameFull strings.Builder
				fmt.Fprintf(&testImageNameFull, "%s/%s", imageBaseURL, testImageName)

				pipelineRunFE := tektonv1.PipelineRun{
					ObjectMeta: metav1.ObjectMeta{
						Name: pipelineRunFEName.String(),
						Namespace: targetNamespace,
						Labels: map[string]string{
							"app": "health",
							"component": "frontend",
							"tag": imageTag,
						},
					},
					Spec: tektonv1.PipelineRunSpec{
						PipelineRef: tektonv1.PipelineRef{Name: fePipelineName},
						Trigger: tektonv1.PipelineTrigger{
							Type: tektonv1.PipelineTriggerTypeManual},
						ServiceAccount: targetServiceAccount,
						Params: []tektonv1.Param{
							{ Name:  "imageTag", Value: imageTag,},
							{ Name:  "nodeTestImage", Value: testImageNameFull.String(),},
						},
						Timeout: &metav1.Duration{Duration: 1 * time.Hour},
						Resources: []tektonv1.PipelineResourceBinding{
							{
								Name: "src",
								ResourceRef: tektonv1.PipelineResourceRef{
									Name: gitResourceName.String(),
								},
							},
							{
								Name: "builtImage",
								ResourceRef: tektonv1.PipelineResourceRef{
									Name: imageResourceFEName.String(),
								},
							},
						},
					},
				}

				// Provision the resources
				_, err = clientset.TektonV1alpha1().PipelineResources(
					targetNamespace).Create(&gitResource)
				if err != nil {
				    log.Fatal(err)
				}
				_, err = clientset.TektonV1alpha1().PipelineResources(
					targetNamespace).Create(&imageResourceAPI)
				if err != nil {
						log.Fatal(err)
				}
				_, err = clientset.TektonV1alpha1().PipelineResources(
					targetNamespace).Create(&imageResourceFE)
				if err != nil {
						log.Fatal(err)
				}

				// Trigger the pipeline runs
				_, err = clientset.TektonV1alpha1().PipelineRuns(
					targetNamespace).Create(&pipelineRunAPI)
				if err != nil {
						log.Fatal(err)
				}
				_, err = clientset.TektonV1alpha1().PipelineRuns(
					targetNamespace).Create(&pipelineRunFE)
				if err != nil {
						log.Fatal(err)
				}

				fmt.Println("Provisioned resources and pipelineruns. 😎 Watch with:")
				fmt.Printf("watch kubectl get all -n %v -l tag=%s\n",
					targetNamespace, imageTag)

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
