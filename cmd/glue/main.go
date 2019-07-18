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
	prCommentTaskName = "pr-comment"
	prStatusUpdateTaskName = "pr-status"
	pullRequestResourceNameTemplate = "pull-request-resource-%s"
)

var pipelineName = map[string]string{
		"api": "dev-test-build-api",
		"frontend": "dev-test-build-frontend",
	}

func process(ctx context.Context, event cloudevents.Event) {
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
	if _, ok := pipelinesMap[pipelineName["api"]]; ok == false {
    log.Fatal("API Pipeline not found")
	}
	if _, ok := pipelinesMap[pipelineName["frontend"]]; ok == false {
    log.Fatal("FE Pipeline not found")
	}
	// Do something if the event is a push
	switch event.SpecVersion() {

	case cloudevents.CloudEventsVersionV02:
		if ec, ok := event.Context.(cloudevents.EventContextV02); ok {

			// Parse the JSON data
			var f interface{}
			err := json.Unmarshal(event.Data.([]byte), &f)
			if err != nil {
					log.Fatal(err)
			}
			eventData := f.(map[string]interface{})

			switch {
			case ec.Type == "dev.knative.source.github.pull_request":
				// Display the event
				display(ctx, event)

				fmt.Printf("Processing a %s\n", ec.Type)

				// Extract the repo URL
				repo := getMapFromJSON("repository", eventData)
				repoURL := getStringFromJSON("html_url", repo)

				// Extract the git sha and PR URL
				var gitSHA string
				var pullRequestURL string
				if ec.Type == "dev.knative.source.github.push" {
					headCommit := getMapFromJSON("head_commit", eventData)
					gitSHA = getStringFromJSON("id", headCommit)
				} else {
					pullRequest := getMapFromJSON("pull_request", eventData)
					pullRequestURL = getStringFromJSON("html_url", pullRequest)
					headCommit := getMapFromJSON("head", pullRequest)
					gitSHA = getStringFromJSON("sha", headCommit)
				}

				// Build the image tag
				var imageTag = gitSHA[0:6]

				// Define Pipeline Resources
				gitResource, gitResourceName := createGitResource(imageTag, repoURL, gitSHA)
				imageResourceAPI, imageResourceAPIName := createImageResource(imageTag,  "api")
				imageResourceFE, imageResourceFEName := createImageResource(imageTag, "frontend")
				// This is not used directly by the pipeline triggered here
				pullRequestResource, _ := createPullRequestResource(imageTag, pullRequestURL)

				// Define Pipeline Runs
				pipelineRunAPI, _ := createTestPipelineRun(imageTag, gitResourceName, imageResourceAPIName, "to-event-tekton-glue", "api")
				pipelineRunFE, _ := createTestPipelineRun(imageTag, gitResourceName, imageResourceFEName, "to-event-tekton-glue", "frontend")

				// Provision the resources
				provisionPipelineResource(gitResource, clientset)
				provisionPipelineResource(imageResourceAPI, clientset)
				provisionPipelineResource(imageResourceFE, clientset)

				// Only provision a PR resource in case of a pull request
				// When a Task triggered by a push completes, it will trigger
				// a TaskRun which will fail because this resource is not there
				if ec.Type == "dev.knative.source.github.pull_request" {
					provisionPipelineResource(pullRequestResource, clientset)
				}

				// Trigger the pipeline runs
				triggerPipelineRun(pipelineRunAPI, clientset)
				triggerPipelineRun(pipelineRunFE, clientset)

				fmt.Println("Provisioned resources and pipelineruns. 😎 Watch with:")
				fmt.Printf("watch kubectl get all -n %v -l tag=%s\n",
					targetNamespace, imageTag)

			case ec.Type == "dev.tekton.event.task.successful" || ec.Type == "dev.tekton.event.task.failed":
				fmt.Println("Processing a Tekton TaskRun event")
				display(ctx, event)

				// Extract the app and tag from the labels
				metadata := getMapFromJSON("metadata", eventData)
				labels := getMapFromJSON("labels", metadata)
				app := getStringFromJSON("app", labels)
				tag := getStringFromJSON("tag", labels)
				taskRunType := getStringFromJSON("type", labels)
				status := getMapFromJSON("status", eventData)
				var taskRun tektonv1.TaskRun
				switch taskRunType {
				case "source-to-image":
					// Extract the image digest from the eventData
					resourcesResults := getSliceFromJSON("resourcesResult", status)
					resourcesResult := resourcesResults[0].(map[string]interface{})
					imageDigest := getStringFromJSON("digest", resourcesResult)
					// Define the taskRun
					taskRun, _ = createPRCommentTaskRun(tag, imageDigest, app)
				case "tox":
					taskID := getStringFromJSON("tekton.dev/pipelineTask", labels)
					conditions := getSliceFromJSON("conditions", status)
					condition := conditions[0].(map[string]interface{})
					taskSuccessCondition := getStringFromJSON("status", condition)
					var taskStatus string
					var taskStatusDescription string
					switch taskSuccessCondition {
					case "True":
						taskStatus = "success"
						taskStatusDescription = "Job executed successfully"
					case "False":
						taskStatus = "failed"
						taskStatusDescription = "Job failed"
					default:
						taskStatus = "unknown"
						taskStatusDescription = "Job in unknown status"
					}
					taskRun, _ = createPRStatusUpdateTaskRun(tag, taskID, taskStatus, taskStatusDescription, app)
				}
				// Trigger the taskRun
				triggerTaskRun(taskRun, clientset)
			default:
				fmt.Println("Ignored non-PR event")
			}
		}

	default:
		fmt.Println("Ignored non-V2 events")
	}
}

func getMapFromJSON(field string, data map[string]interface{}) map[string]interface{} {
	result, ok := data[field].(map[string]interface{})
	if ok == false {
		log.Fatalf("Unexpected data extracting %s", field)
		return nil
	}
	return result
}

func getSliceFromJSON(field string, data map[string]interface{}) []interface{} {
	result, ok := data[field].([]interface{})
	if ok == false {
		log.Fatalf("Unexpected data extracting %s", field)
		return nil
	}
	return result
}

func getStringFromJSON(field string, data map[string]interface{}) string {
	result, ok := data[field].(string)
	if ok == false {
		log.Fatalf("Unexpected data extracting %s", field)
		return ""
	}
	return result
}

func createGitResource(tag, url, revision string) (tektonv1.PipelineResource, string) {
	// Build the git resource
	var gitResourceName strings.Builder
	fmt.Fprintf(&gitResourceName, "git-resource-%s", tag)

	return tektonv1.PipelineResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: gitResourceName.String(),
			Namespace: targetNamespace,
			Labels: map[string]string{
				"app": "health",
				"tag": tag,
			},
		},
		Spec: tektonv1.PipelineResourceSpec{
			Type: tektonv1.PipelineResourceTypeGit,
			Params: []tektonv1.Param{
				tektonv1.Param {
					Name: "url",
					Value: url,
				},
				tektonv1.Param {
					Name: "revision",
					Value: revision,
				},
			},
		},
	}, gitResourceName.String()
}

func createImageResource(tag, app string) (tektonv1.PipelineResource, string) {
	// Build the image resource
	var imageResourceName strings.Builder
	fmt.Fprintf(&imageResourceName, "image-resource-%s-%s", app, tag)
	var imageURL strings.Builder
	fmt.Fprintf(&imageURL, "%s/health-%s", imageBaseURL, app)

	return tektonv1.PipelineResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: imageResourceName.String(),
			Namespace: targetNamespace,
			Labels: map[string]string{
				"app": "health",
				"tag": tag,
			},
		},
		Spec: tektonv1.PipelineResourceSpec{
			Type: tektonv1.PipelineResourceTypeImage,
			Params: []tektonv1.Param{
				tektonv1.Param {
					Name: "url",
					Value: imageURL.String(),
				},
			},
		},
	}, imageResourceName.String()
}

func createPullRequestResource(tag, url string) (tektonv1.PipelineResource, string) {
	// Build the image resource
	var pullRequestResourceName strings.Builder
	fmt.Fprintf(&pullRequestResourceName, pullRequestResourceNameTemplate, tag)

	return tektonv1.PipelineResource{
		ObjectMeta: metav1.ObjectMeta{
			Name: pullRequestResourceName.String(),
			Namespace: targetNamespace,
			Labels: map[string]string{
				"app": "health",
				"tag": tag,
			},
		},
		Spec: tektonv1.PipelineResourceSpec{
			Type: tektonv1.PipelineResourceTypePullRequest,
			Params: []tektonv1.Param{
				tektonv1.Param {
					Name: "url",
					Value: url,
				},
			},
			SecretParams: []tektonv1.SecretParam{
				tektonv1.SecretParam{
					FieldName: "githubToken",
					SecretName: "githubsecret",
					SecretKey: "accessToken",
				},
			},
		},
	}, pullRequestResourceName.String()
}

func provisionPipelineResource(resource tektonv1.PipelineResource, clientset *tektonv1client.Clientset) {
	_, err := clientset.TektonV1alpha1().PipelineResources(
		targetNamespace).Create(&resource)
	if err != nil {
			log.Fatal(err)
	}
}

func createTestPipelineRun(tag, gitResourceName, imageResourceName, cloudEventResourceName, app string) (tektonv1.PipelineRun, string){
	// Build the API PipelineRun
	var pipelineRunName strings.Builder
	fmt.Fprintf(&pipelineRunName, "pipeline-run-%s-%s", app, tag)

	return tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: pipelineRunName.String(),
			Namespace: targetNamespace,
			Labels: map[string]string{
				"app": "health",
				"component": app,
				"tag": tag,
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			PipelineRef: tektonv1.PipelineRef{Name: pipelineName[app]},
			ServiceAccount: targetServiceAccount,
			Params: []tektonv1.Param{{
				Name:  "imageTag", Value: tag,
			},},
			Timeout: &metav1.Duration{Duration: 1 * time.Hour},
			Resources: []tektonv1.PipelineResourceBinding{
				{
					Name: "src",
					ResourceRef: tektonv1.PipelineResourceRef{
						Name: gitResourceName,
					},
				},
				{
					Name: "builtImage",
					ResourceRef: tektonv1.PipelineResourceRef{
						Name: imageResourceName,
					},
				},
				{
					Name: "prTrigger",
					ResourceRef: tektonv1.PipelineResourceRef{
						Name: cloudEventResourceName,
					},
				},
			},
		},
	}, pipelineRunName.String()
}

func triggerPipelineRun(pipelineRun tektonv1.PipelineRun, clientset *tektonv1client.Clientset) {
	_, err := clientset.TektonV1alpha1().PipelineRuns(
		targetNamespace).Create(&pipelineRun)
	if err != nil {
			log.Fatal(err)
	}
}

func createPRCommentTaskRun(tag, digest, app string) (tektonv1.TaskRun, string){
	// Build the API PipelineRun
	var taskRunName strings.Builder
	fmt.Fprintf(&taskRunName, "task-run-pr-comment-%s-%s", app, tag)

	var pullRequestResourceName strings.Builder
	fmt.Fprintf(&pullRequestResourceName, pullRequestResourceNameTemplate, tag)

	var comment strings.Builder
	fmt.Fprintf(&comment, "Published image https://%s/health-%s@%s", imageBaseURL, app, digest)

	return tektonv1.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: taskRunName.String(),
			Namespace: targetNamespace,
			Labels: map[string]string{
				"app": "health",
				"component": app,
				"tag": tag,
			},
		},
		Spec: tektonv1.TaskRunSpec{
			Inputs: tektonv1.TaskRunInputs{
				Resources: []tektonv1.TaskResourceBinding{
					{
						Name: "pr",
						ResourceRef: tektonv1.PipelineResourceRef{
							Name: pullRequestResourceName.String(),
						},
					},
				},
				Params: []tektonv1.Param{
					{
						Name: "comment",
						Value: comment.String(),
					},
				},
			},
			Outputs: tektonv1.TaskRunOutputs{
				Resources: []tektonv1.TaskResourceBinding{
					{
						Name: "pr",
						ResourceRef: tektonv1.PipelineResourceRef{
							Name: pullRequestResourceName.String(),
						},
					},
				},
			},
			TaskRef: &tektonv1.TaskRef{Name: prCommentTaskName},
			ServiceAccount: targetServiceAccount,
			Timeout: &metav1.Duration{Duration: 1 * time.Hour},
		},
	}, taskRunName.String()
}

func createPRStatusUpdateTaskRun(tag, taskID, taskStatus, taskStatusDescription, app string) (tektonv1.TaskRun, string){
	// Build the API PipelineRun
	var taskRunName strings.Builder
	fmt.Fprintf(&taskRunName, "task-run-pr-status-%s-%s", taskID, tag)

	var pullRequestResourceName strings.Builder
	fmt.Fprintf(&pullRequestResourceName, pullRequestResourceNameTemplate, tag)

	return tektonv1.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			Name: taskRunName.String(),
			Namespace: targetNamespace,
			Labels: map[string]string{
				"app": "health",
				"component": app,
				"tag": tag,
			},
		},
		Spec: tektonv1.TaskRunSpec{
			Inputs: tektonv1.TaskRunInputs{
				Resources: []tektonv1.TaskResourceBinding{
					{
						Name: "pr",
						ResourceRef: tektonv1.PipelineResourceRef{
							Name: pullRequestResourceName.String(),
						},
					},
				},
				Params: []tektonv1.Param{
					{
						Name: "id",
						Value: taskID,
					},
					{
						Name: "status",
						Value: taskStatus,
					},
					{
						Name: "description",
						Value: taskStatusDescription,
					},
					{
						Name: "url",
						Value: "https://fake.base.url",
					},
				},
			},
			Outputs: tektonv1.TaskRunOutputs{
				Resources: []tektonv1.TaskResourceBinding{
					{
						Name: "pr",
						ResourceRef: tektonv1.PipelineResourceRef{
							Name: pullRequestResourceName.String(),
						},
					},
				},
			},
			TaskRef: &tektonv1.TaskRef{Name: prStatusUpdateTaskName},
			ServiceAccount: targetServiceAccount,
			Timeout: &metav1.Duration{Duration: 1 * time.Hour},
		},
	}, taskRunName.String()
}

func triggerTaskRun(taskRun tektonv1.TaskRun, clientset *tektonv1client.Clientset) {
	_, err := clientset.TektonV1alpha1().TaskRuns(
		targetNamespace).Create(&taskRun)
	if err != nil {
			log.Fatal(err)
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
