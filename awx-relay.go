package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type RelayCommand struct {
	Port  string `short:"p" long:"port" description:"Port for the webserver to listen on" default:"8080"`
	Debug bool   `short:"d" long:"debug" description:"Enables Gin debug mode"`
}

var relayCommand RelayCommand

func init() {
	parser.AddCommand("relay", "Starts the AWX relay webserver", "Starts the AWX relay webserver", &relayCommand)
}

// HostData is the data received from a midtier host
type HostData struct {
	Fqdn           string `json:"fqdn" binding:"required"`
	BreakglassID   int    `json:"breakglassid" binding:"required"`
	BreakglassName string `json:"breakglassname" binding:"required"`
	BaselineID     int    `json:"baselineid" binding:"required"`
	BaselineName   string `json:"baselinename" binding:"required"`
	InvID          int    `json:"invid" binding:"required"`
	InvName        string `json:"invname" binding:"required"`
	DesiredRelease string `json:"desiredrelease" required:"true"`
	Reboot         string `json:"reboot" required:"true"`
	Type           string `json:"type"`
	Facility       string `json:"facility"`
	Mock           string `json:"mock"`
}

// sets up API endpoints and their related functions
func (r *RelayCommand) Execute(args []string) error {
	if !r.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	//create our instance
	router := gin.Default()

	//disable proxy handling, it complains if you trust all which is set by default
	router.SetTrustedProxies(nil)

	//create enpoint to build a host
	router.POST("/build/", build)

	//set the IP and port to listen on
	ipPort := fmt.Sprintf("0.0.0.0:%v", r.Port)

	//start the webserver
	if err := router.Run(ipPort); err != nil {
		return fmt.Errorf("startWebserver(): router.Run(): %w", err)
	} else {
		fmt.Printf("Started AWX-Relay webserver on port %v", r.Port)
	}

	return nil
}

// ReadMidtierFqdn is a quick hack for fixing the logging of 'relay' mode, otherwise all
// PrintStatus() calls would need to accept 'fqdn' as a parameter when run in 'relay' mode
// but this isnt't needed in midtier or internal mode since it logs to the host it's run on
func ReadMidtierFqdn() string {
	return fqdn
}

func build(c *gin.Context) {
	var input HostData

	// tells PrintStatus() where to log output, for the relay all output related
	// to the host being built is written to /var/log/awx-relay/[fqdn].log
	relay = true

	//binding our received data (c) with a struct (input)
	//verifying the JSON in the process
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"build(): c.ShouldBindJSON(): ": err.Error()})
		return
	}

	jobVars := JobVars{
		BreakglassID:   input.BreakglassID,
		BreakglassName: input.BreakglassName,
		BaselineID:     input.BaselineID,
		BaselineName:   input.BaselineName,
		InvID:          input.InvID,
		InvName:        input.InvName,
		DesiredRelease: input.DesiredRelease,
		Reboot:         input.Reboot,
		FQDN:           input.Fqdn,
		Type:           input.Type,
		Facility:       input.Facility,
		Mock:           input.Mock,
	}

	// quick hack for PrintStatus()
	fqdn = jobVars.FQDN

	fmt.Printf("INFO: Launching AWX jobs for %v...\n", input.Fqdn)
	fmt.Printf("INFO: See /var/log/awx-relay/%v.log for more info\n", jobVars.FQDN)

	// kick off breakglass, wait until it finishes, and then kick off baseline
	status, jobErr := KickoffJobs(input.Fqdn, jobVars, jobVars.Mock)
	PrintStatus("INFO: Sending the build status to the host...")
	if jobErr != nil {
		if strings.Contains(jobErr.Error(), "failed") {
			c.JSON(http.StatusOK, jobErr.Error())
		} else {
			if strings.Contains(jobErr.Error(), "ERROR") {
				PrintStatus("INFO: letting the client know that the job failed...")
				c.JSON(http.StatusInternalServerError, jobErr.Error())
				return
			} else {
				PrintStatus(fmt.Sprintf("ERROR: build(): %v", jobErr))
				PrintStatus("INFO: letting the client know that the job failed...")
				c.JSON(http.StatusInternalServerError, jobErr.Error())
				return
			}
		}
	} else if status == "successful" {
		PrintStatus(fmt.Sprintf("INFO: %v and %v were executed successfully", jobVars.BreakglassName, jobVars.BaselineName))
		c.JSON(http.StatusOK, status)
		return
	} else {
		PrintStatus(fmt.Sprintf("INFO: unknown job status %v", status))
		c.JSON(http.StatusInternalServerError, status)
		return
	}

}
