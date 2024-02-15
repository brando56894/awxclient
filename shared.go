package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	awxGo "github.com/Colstuwjx/awx-go"
)

type JobVars struct {
	BreakglassID    int
	BreakglassName  string
	BaselineID      int
	BaselineName    string
	InvID           int
	InvName         string
	DesiredRelease  string
	Reboot          string
	FQDN            string
	BreakglassJobID string
	BaselineJobID   string
	Type            string
	Facility        string
	Mock            string
	Relay           string
}

// global so it doesn't have to be passed around a million times
var awx *awxGo.AWX

// CheckDistro() checks whether the host is running CentOS or Rocky Linux
func CheckDistro() (string, error) {
	if _, err := os.Stat("/etc/rocky-release"); errors.Is(err, os.ErrNotExist) {
		return "centos", nil
	} else if _, err := os.Stat("/etc/rocky-release"); errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("CheckDistro(): %w", err)
	} else {
		return "rocky", nil
	}
}

// GetGroupID gets the group id of the matching datacenter in an inventory
func GetGroupID(jobVars JobVars) (int, error) {
	var groupID int

	//TODO: Add in plain English error message when the group doesn't exist
	result, _, err := awx.GroupService.ListGroups(map[string]string{"name": jobVars.Facility})
	if err != nil {
		return groupID, fmt.Errorf("getGroupID(): awx.GroupService.ListGroups(): %w", err)
	}

	for _, value := range result {
		//TODO: these inventory IDs probably shouldn't be hardcoded
		if jobVars.InvID == 44 && value.Inventory == 44 {
			groupID = value.ID
			break
		} else if jobVars.InvID == 392 && value.Inventory == 392 {
			groupID = value.ID
			break
		} else if jobVars.InvID == 513 && value.Inventory == 513 {
			groupID = value.ID
			break
		} else if jobVars.InvID == 516 && value.Inventory == 516 {
			groupID = value.ID
			break
		}
	}

	return groupID, nil
}

// DoesHostExist checks through the list of hosts in AWX for the FQDN and ensures it exists in the correct inventory
func DoesHostExist(fqdn string, jobVars JobVars) error {
	result, _, err := awx.HostService.ListHosts(map[string]string{"name": fqdn})
	if err != nil {
		return err
	}

	var invName string

	for _, host := range result {
		if host.Name == fqdn {
			// host can be in multiple inventories
			switch host.Inventory {
			// TODO: these inventory IDs probably shouldn't be hardcoded
			case 44:
				if strings.Contains(jobVars.DesiredRelease, "7.") && jobVars.Type == "internal" {
					invName = "Foreman_Hosts"
				}
			case 392:
				if strings.Contains(jobVars.DesiredRelease, "8.") && jobVars.Type == "internal" {
					invName = "Rocky Foreman"
				}
			case 513:
				if jobVars.Type == "midtier" {
					invName = "Midtier-Baremetal"
				}
			case 516:
				if jobVars.Type == "edge" {
					invName = "Edge-Baremetal"
				}
			}
			PrintStatus(fmt.Sprintf("INFO: Found %v in inventory %v", fqdn, invName))
			return nil
		}
	}

	PrintStatus(fmt.Sprintf("INFO: Couldn't find %v in any inventory", fqdn))
	PrintStatus("INFO: Attempting to create it...")
	if err := CreateHost(fqdn, jobVars); err != nil {
		if strings.Contains(err.Error(), "ERROR") {
			return err
		}
		return fmt.Errorf("doesHostExist(): %w", err)
	}

	return nil
}

// CreateHost adds an FQDN to an AWX inventory
func CreateHost(fqdn string, jobVars JobVars) error {
	var err error

	// if we're building an internal host the incoming jobVars is empty, fill it out
	if jobVars.Type == "internal" {
		jobVars, err = ReadAwxVars() //read from a local file
	}

	if err != nil {
		return fmt.Errorf("createHost(): %w", err)
	}

	_, err = awx.HostService.CreateHost(map[string]interface{}{
		"name":        fqdn,
		"inventory":   jobVars.InvID,
		"description": "Host built by Foreman",
		"enabled":     true,
	}, map[string]string{})

	if err != nil {
		return fmt.Errorf("createHost(): awx.HostService.CreateHost(): %w", err)
	}

	if err = AddHostToGroup(fqdn, jobVars); err != nil {
		if strings.Contains(err.Error(), "ERROR") {
			return err
		}
		return fmt.Errorf("createHost(): %w", err)
	}

	PrintStatus(fmt.Sprintf("INFO: Successfully created %v and added it to the %v inventory", fqdn, jobVars.InvName))

	return nil
}

// AddHostToGroup associates an FQDN with an AWX inventory group
func AddHostToGroup(fqdn string, jobVars JobVars) error {
	groupID, err := GetGroupID(jobVars)

	if err != nil {
		return fmt.Errorf("addHostToGroup(): %w", err)
	}

	// getting the ID of the host
	hosts, _, err := awx.HostService.ListHosts(map[string]string{"name": fqdn})
	if err != nil {
		return fmt.Errorf("addHostToGroup(): awx.HostService.ListHosts(): %w", err)
	}

	var hostID int
	for _, host := range hosts {
		if host.Name == fqdn {
			hostID = host.ID
		}
	}

	if hostID == 0 {
		return errors.New("addHostToGroup(): can't find host ID")
	}

	_, err = awx.HostService.AssociateGroup(hostID, map[string]interface{}{"id": groupID}, map[string]string{})

	if err != nil && strings.Contains(err.Error(), "Bad Request") || err != nil && strings.Contains(err.Error(), "404") {
		return fmt.Errorf((fmt.Sprintf("Ensure %v group exists in the %v inventory", jobVars.Facility, jobVars.InvName)))
	}

	if err != nil {
		return fmt.Errorf("addHostToGroup(): awx.HostService.AssociateGroup(): %w", err)
	}

	PrintStatus(fmt.Sprintf("INFO: Successfully added %v to the %v group in %v", fqdn, jobVars.Facility, jobVars.InvName))

	return nil
}

// LaunchJob kicks off an AWX job template
func LaunchJob(fqdn, templateName string, templateID int, params map[string]interface{}) error {
	successFile := fmt.Sprintf("/var/tmp/%v-%v.success", fqdn, templateName)

	if DoesFileExist(successFile) {
		// job already executed
		return fmt.Errorf(fmt.Sprintf("LaunchJob(): %v exists not launching %v", successFile, templateName))
	}

	PrintStatus(fmt.Sprintf("INFO: Kicking off %v...", templateName))
	result, err := awx.JobTemplateService.Launch(templateID, params, map[string]string{})
	if err != nil {
		return fmt.Errorf("LaunchJob: awx.JobTemplateService.Launch(): %v", err)
	}

	// checks the status of the job every 10 seconds until it completes or errors out
	jobSummary, jobErr := GetStatus(result.ID)
	if jobErr != nil {
		// job failure
		if strings.Contains(jobErr.Error(), "failed") {
			return jobErr
		} else {
			// software issue
			return fmt.Errorf("launchJob(): %w", jobErr)
		}
	}

	PrintStatus(fmt.Sprintf("INFO: Status of %v: %v", templateName, jobSummary.Status))

	os.Create(successFile)

	return nil
}

// GetStatus continually checks the job status until its either no longer pending or running, or in error
func GetStatus(jobID int) (*awxGo.HostSummaryJob, error) {
	var job []awxGo.HostSummary
	var jobHostSummary *awxGo.HostSummaryJob
	var err error
	var sleeptime int

	PrintStatus("INFO: Sleeping for a few minutes so the job can run...")
	time.Sleep(30 * time.Second)
	sleeptime += 30

	for {
		job, _, err = awx.JobService.GetHostSummaries(jobID, map[string]string{})
		if err != nil {
			return nil, err
		}

		for _, value := range job {
			if value.Failed || value.SummaryFields.Job.Status == "failed" {
				return jobHostSummary, fmt.Errorf("%v failed at %v. Check job id %v for more info",
					value.SummaryFields.Job.JobTemplateName, GetTime("short"), value.Job)
			}
			if value.SummaryFields.Job.Status != "pending" && value.SummaryFields.Job.Status != "running" {
				jobHostSummary = value.SummaryFields.Job
				return jobHostSummary, nil
			}
		}

		// check again in ten seconds
		time.Sleep(10 * time.Second)
		sleeptime += 10

		if sleeptime > 1800 {
			return jobHostSummary, fmt.Errorf("job ID %v didn't complete after a half hour, something is probably wrong", jobID)
		}
	}
}

// GetTime gets the current time in the current timezone
func GetTime(length string) string {
	current_time := time.Now()
	if length == "full" {
		return current_time.Format(time.RFC1123)
	} else {
		return fmt.Sprintf("%v:%v:%v GMT", current_time.Hour(), current_time.Minute(), current_time.Second())
	}

}

func DoesFileExist(path string) bool {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false
		}
	}
	return true
}

// PrintStatus writes the output to STDOUT or to a file based on execution mode
func PrintStatus(msg string) error {
	// output to a logfile
	if relay {
		logfilePath := "/var/log/awx-relay"
		logfile := fmt.Sprintf(logfilePath+"/%v.log", ReadMidtierFqdn())
		// ensure the log directory exists before we attempt to write to it
		if _, err := os.Stat("/var/log/awx-relay"); errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir("/var/log/awx-relay", 0775); err != nil {
				return fmt.Errorf("PrintStatus(): os.Mkdir(): %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("PrintStatus()): os.Stat(): %w", err)
		}

		// if the file doesn't exist, create it and write to it, otherwise append to it
		f, err := os.OpenFile(logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("PrintStatus(): os.Open(): %w", err)
		}
		var writeErr error
		fileData, err := os.ReadFile(logfile)
		if err != nil {
			return fmt.Errorf("PrintStatus(): os.Readfile(): %w", err)
		}
		if !strings.Contains(string(fileData), "Build Date") {
			_, writeErr = f.WriteString(fmt.Sprintf("Build Date: %v\n", GetTime("full")))
			if writeErr != nil {
				f.Close() // ignore error; Write error takes precedence
				return fmt.Errorf("PrintStatus(): f.WriteString(): %w", err)
			}
		}
		if strings.Contains(msg, "\n") {
			_, writeErr = f.WriteString(msg)
		} else {
			_, writeErr = f.WriteString(msg + "\n")
		}
		if writeErr != nil {
			f.Close() // ignore error; Write error takes precedence
			return fmt.Errorf("PrintStatus(): f.WriteString(): %w", err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf(fmt.Sprintf("PrintStatus(): f.Close(): %v", err))
		}
		// print to STDOUT, which is the systemd journal for awx-relay.service
		// when started as a service, otherwise it's the terminal
	} else {
		fmt.Printf("%v\n", msg)
	}

	return nil
}

func ReadFile(path string) (map[string]string, error) {
	vars := make(map[string]string)

	// does the file exist?
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return vars, fmt.Errorf("ReadFile(): os.Stat(): %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return vars, fmt.Errorf("ReadFile(): os.ReadFile(): %v", err)
	}

	// grab all the data matching a "WORD=ANYTEXT" pattern
	r := regexp.MustCompile(`(?m).+=.+`)
	po := r.FindAllStringSubmatch(string(data), -1)

	// take each regex result (which is a line of text from the file), split it at = then discard the =
	// and store the split text into the map, with the left side being the key, right side being the value
	for _, value := range po {
		ss := strings.Split(value[0], "=")
		vars[strings.ToLower(ss[0])] = ss[1]
	}

	return vars, nil
}

// AwxClientSetup sets up our modified client instance which can be re-used
func AwxClientSetup() (*awxGo.AWX, error) {
	// grab our AWX credentials which were written by Foreman
	data, err := ReadFile("/var/tmp/.tower_creds")
	if err != nil {
		return nil, fmt.Errorf("awxClientSetup(): %w", err)
	}

	var username, password string
	for key, value := range data {
		if key == "user" {
			username = value
		} else if key == "pass" {
			password = value
		}
	}

	// disable SSL verification because it complains about an invalid cert
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Transport: transport}

	// create our AWX object, using the modified client we created above
	awx := awxGo.NewAWX("https://awx.internaldomain.co", username, password, client)

	return awx, nil
}
