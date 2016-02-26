package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

func main() {
	flag.Parse()
	project = *projectName
	if err := validateFlags(); err != nil {
		log.Println(err)
		flag.Usage()
		os.Exit(2)
	}
	c := getClient()
	zone := GetZone(c, *groupName)
	tt := GetTemplate(c, *templateName)
	nt := UpdateImage(tt, GetHash(*imageName))
	InsertTemplate(c, nt)
	SetTemplate(c, *groupName, zone, nt.SelfLink)
	RecreateAll(c, *groupName, zone)
	log.Printf("Successfully deployed %q to %s:%s", GetHash(*imageName), project, *groupName)
}

type opError struct {
	oe *compute.OperationError
}

func (oe *opError) Error() string {
	b, _ := json.MarshalIndent(oe.oe, "", "  ")

	return "operation error: \n" + string(b)
}

func WaitOp(c *compute.Service, op *compute.Operation, state string) error {
	var err error
	for op.Status != "DONE" {
		time.Sleep(1 * time.Second)
		switch {
		case op.Zone != "":
			op, err = c.ZoneOperations.Get(project, op.Zone, op.Name).Do()
			if err != nil {
				return err
			}
		case op.Region != "":
			op, err = c.RegionOperations.Get(project, op.Region, op.Name).Do()
			if err != nil {
				return err
			}
		default:
			op, err = c.GlobalOperations.Get(project, op.Name).Do()
			if err != nil {
				return err
			}
		}
		log.Printf("%s: %v", state, op.Progress)
	}
	if op.Error != nil && len(op.Error.Errors) != 0 {
		return &opError{oe: op.Error}
	}
	return nil
}

func GetTemplate(c *compute.Service, name string) *compute.InstanceTemplate {
	tt, err := c.InstanceTemplates.Get(project, name).Do()
	if err != nil {
		log.Panicf("Cannot get instance template %q of project %q: %v", name, project, err)
		return nil
	}
	return tt
}

func InsertTemplate(c *compute.Service, template *compute.InstanceTemplate) {
	op, err := c.InstanceTemplates.Insert(project, template).Do()
	if ge, ok := err.(*googleapi.Error); ok && ge.Code == http.StatusConflict {
		log.Printf("template %q already exists, NOT adding it again", template.Name)
		return
	}
	if err != nil {
		log.Panicf("Cannot insert instance template %q of project %q: %v", template.Name, project, err)
		return
	}
	if err := WaitOp(c, op, "creating template"); err != nil {
		log.Panicf("Op error: cannot insert instance template %q of project %q: %v", template.Name, project, err)
		return
	}
}

func SetTemplate(c *compute.Service, group, zone, newTemplate string) {
	tr := &compute.InstanceGroupManagersSetInstanceTemplateRequest{
		InstanceTemplate: newTemplate,
	}
	op, err := c.InstanceGroupManagers.SetInstanceTemplate(project, zone, group, tr).Do()
	if ge, ok := err.(*googleapi.Error); ok {
		log.Print(ge.Code)
	}
	if err != nil {
		log.Panicf("Cannot set instance template %q for %q of project %q: %v", newTemplate, group, project, err)
		return
	}
	if err := WaitOp(c, op, "updating group"); err != nil {
		log.Panicf("Op error: cannot set instance template %q for %q of project %q: %v", newTemplate, group, project, err)
		return
	}
}

func ListManagedInstances(c *compute.Service, group, zone string, acceptActions, rejectActions []string) ([]string, error) {
	var instances []string
	mi, err := c.InstanceGroupManagers.ListManagedInstances(project, zone, group).Do()
	if err != nil {
		return nil, err

	}
	for _, inst := range mi.ManagedInstances {
		if inst.InstanceStatus != "RUNNING" {
			continue
		}

		accept := true

		for _, a := range rejectActions {
			if inst.CurrentAction == a {
				accept = false
				break
			}
		}

		if !accept {
			continue
		}

		accept = len(acceptActions) == 0
		for _, a := range acceptActions {
			if inst.CurrentAction == a {
				accept = true
			}
		}

		if !accept {
			continue
		}
		instances = append(instances, inst.Instance)
	}
	return instances, nil

}

func RecreateAll(c *compute.Service, group, zone string) {
	instances, err := ListManagedInstances(c, group, zone, nil, []string{"RECREATING", "DELETING"})
	if err != nil {
		log.Panicf("Cannot cannot list managed instances for %s:%s of project %s %v", group, zone, project, err)
		return
	}

	// Don't re-create empty pool members
	if len(instances) == 0 {
		return
	}

	// TODO recreate only some of them and only, if the used instance-template is too old
	// But this is good enough for single instance pools for now
	rcReq := &compute.InstanceGroupManagersRecreateInstancesRequest{
		Instances: instances,
	}
	op, err := c.InstanceGroupManagers.RecreateInstances(project, zone, group, rcReq).Do()
	if err != nil {
		log.Panicf("Cannot re-create instances in %q of project %q: %v", group, project, err)
		return
	}
	log.Printf("re-creating %d instances", len(instances))
	if err := WaitOp(c, op, "re-create instances"); err != nil {
		log.Panicf("Op error: Cannot re-create instances in %q of project %q: %v", group, project, err)
		return
	}

	for {
		accept := []string{"RECREATING"}
		busy, err := ListManagedInstances(c, group, zone, accept, nil)
		if err != nil {
			log.Panicf("Cannot cannot re-list managed instances for %s:%s of project %s %v", group, zone, project, err)
			return

		}
		if len(busy) == 0 {
			return
		}
		log.Printf("%d/%d still re-creating...", len(busy), len(instances))
		time.Sleep(1 * time.Second)
	}
}

func GetHash(name string) string {
	i := strings.LastIndex(name, "-")
	if i < 0 {
		return ""
	}
	return name[i+1:]
}

func GetZone(c *compute.Service, group string) string {
	var zones []string
	err := c.Zones.List(project).Pages(context.Background(), func(zl *compute.ZoneList) error {
		for _, z := range zl.Items {
			zones = append(zones, z.Name)
		}
		return nil
	})
	if err != nil {
		log.Panicf("Cannot list zones for project %q: %v", project, err)
		return ""
	}

	for _, z := range zones {
		ig, err := c.InstanceGroups.Get(project, z, group).Do()
		if ge, ok := err.(*googleapi.Error); ok && ge.Code == http.StatusNotFound {
			continue
		}
		if err != nil {
			log.Panicf("Cannot get zone of group %q for project %q: %v", group, project, err)
			return ""
		}
		if ig.Name == group {
			return z
		}

	}
	if len(zones) == 0 {
		log.Panicf("compute engine not enabled for project %q", project)
		return ""
	}
	log.Panicf("Cannot group %q for project %q not found in %v", group, project, zones)
	return ""
}

func UpdateImage(old *compute.InstanceTemplate, hash string) *compute.InstanceTemplate {
	t := new(compute.InstanceTemplate)
	t.Name = strings.Replace(old.Name, GetHash(old.Name), hash, 1)
	t.SelfLink = strings.Replace(old.SelfLink, GetHash(old.SelfLink), hash, 1)
	t.Properties = old.Properties
	for _, m := range t.Properties.Metadata.Items {
		if m.Key == "app_version" {
			m.Value = &hash
		}
	}
	image := old.Properties.Disks[0].InitializeParams.SourceImage
	t.Properties.Disks[0].InitializeParams.SourceImage = strings.Replace(image, GetHash(image), hash, 1)
	return t
}

var (
	templateName = flag.String("template", "", "Template where we take our defaults from")
	imageName    = flag.String("image", "", "System image to which we want to update")
	groupName    = flag.String("group", "", "instance group which we want to update")
	projectName  = flag.String("project", "", "project which we talk about")
)

var project string

func validateFlags() error {
	switch {
	case *projectName == "":
		return fmt.Errorf("%s name cannot be empty", "project")
	case *templateName == "":
		return fmt.Errorf("%s name cannot be empty", "template")
	case !strings.HasSuffix(*templateName, "-defaults"):
		return fmt.Errorf("%s name must end in -defaults", "template")
	case *imageName == "":
		return fmt.Errorf("%s name cannot be empty", "image")
	case GetHash(*imageName) == "":
		return fmt.Errorf("%s name does not contain git hash", "image")
	case *groupName == "":
		return fmt.Errorf("%s name cannot be empty", "group")
	}
	return nil
}

func getClient() *compute.Service {
	const (
		computeScope = compute.ComputeScope
	)

	ctx := context.Background()
	oauthHttpClient, err := google.DefaultClient(ctx, computeScope)
	if err != nil {
		log.Panicf("Cannot create OAuth2 client for scope %s: %v", computeScope, err)
		return nil
	}
	computeService, err := compute.New(oauthHttpClient)
	if err != nil {
		log.Panicf("Cannot create Compute Services client: %v", err)
		return nil
	}
	return computeService
}
