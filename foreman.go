package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/go-ping/ping"
)

type ForemanOptions struct {
	RelayPort string `short:"r" long:"relayport" description:"If the default port of the AWX relay was changed, set it here" default:"8080"`
	Mock      string `short:"m" long:"mock" description:"Runs all the necessary functions, but doesn't actually launch any jobs and returns 'success'. Requires an FQDN as an argument."`
	File      string `short:"f" long:"file" description:"Alternate location to read an AWX vars file from, can be local or via a web request. Requires JSON formatting."`
}

type ForemanVars struct {
	Server   string
	Facility string
	Type     string
	BuildIP  string
	OSmajor  string
	OSminor  string
	Relay    string
}

type AwxVars struct {
	BaselineName   string `json:"baselinename"`
	BaselineID     int    `json:"baselineid"`
	BreakglassName string `json:"breakglassname"`
	BreakglassID   int    `json:"breakglassid"`
	InventoryName  string `json:"inventoryname"`
	InventoryID    int    `json:"inventoryid"`
	Reboot         string `json:"reboot"`
}

var foremanOptions ForemanOptions

// Hacky fix for PrintStatus()
var fqdn string
var hostType string

func init() {
	parser.AddCommand("foreman", "Execute Foreman post-install AWX jobs", "Launches Breakglass and Baseline AWX jobs for the respective environment", &foremanOptions)
}

// Main function for the the Foreman subcommand
func (f *ForemanOptions) Execute(args []string) error {
	// getting the hostname to execute jobs on
	if f.Mock != "" {
		fqdn = f.Mock
	} else {
		var err error
		if fqdn, err = os.Hostname(); err != nil {
			fmt.Println("ERROR: os.Hostname(): ", err)
			os.Exit(1)
		}
	}

	// "pre-flight" checks to ensure the jobs will launch correctly
	jobVars, err := Prelaunch(fqdn)
	hostType = jobVars.Type
	if err != nil {
		if strings.Contains(err.Error(), "failed") {
			fmt.Println(err)
		}
		if strings.Contains(err.Error(), "(") {
			fmt.Println(err)
		} else {
			fmt.Println(err)
		}
		os.Exit(1)
	}

	var status string

	if hostType == "internal" {
		status, err = internal(jobVars)
	} else if hostType == "midtier" || hostType == "edge" {
		status, err = client(jobVars)
	}

	if err != nil {
		PrintStatus(fmt.Sprintf("ERROR: %v", err))
		os.Exit(1)
	}

	if strings.Contains(status, "successful") {
		if err := CleanUp(jobVars.Reboot, foremanOptions.Mock); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		msg := fmt.Sprintf("INFO: %v and %v completed successfully", jobVars.BreakglassName, jobVars.BaselineName)
		PrintStatus(msg)
	}

	return nil
}

func internal(jobVars JobVars) (string, error) {
	// kick off breakglass, wait until it finishes and check its status, and then kick off baseline
	status, jobErr := KickoffJobs(fqdn, jobVars, foremanOptions.Mock)
	if jobErr != nil {
		if strings.Contains(jobErr.Error(), "failed") {
			return "", jobErr
		}
		if strings.Contains(jobErr.Error(), "(") {
			return "", fmt.Errorf("internal(): %w", jobErr)
		} else {
			return "", jobErr
		}
	}

	return status, nil
}

func client(jobVars JobVars) (string, error) {
	// add in the fqdn
	jobVars.FQDN = fqdn

	if foremanOptions.Mock != "" {
		jobVars.Mock = foremanOptions.Mock
	}

	// encode our object in JSON which can then be sent to the relay
	jsonData, jsonErr := json.Marshal(jobVars)
	if jsonErr != nil {
		return "", fmt.Errorf("client(): json.Marshal(): %w", jsonErr)
	}

	PrintStatus("INFO: Sending collected data to the AWX Relay...")
	mtrelayHostURL := fmt.Sprintf("http://%v:%v/build", jobVars.Relay, foremanOptions.RelayPort)
	requestData, requestErr := http.NewRequest(http.MethodPost, mtrelayHostURL, bytes.NewReader(jsonData))

	if requestErr != nil {
		return "", fmt.Errorf("client(): http.NewRequest(): %w", requestErr)
	}

	r, err := http.DefaultClient.Do(requestData)
	if err != nil {
		return "", fmt.Errorf("client(): http.DefaultClient(): %w", err)
	}

	respBody, err := io.ReadAll(r.Body)
	if err != nil {
		return "", fmt.Errorf("client() io.ReadAll(): %w", err)
	}

	pteErrorMsg := "There is an issue with the AWX Relay, please reach out to Platform Engineering.\n "

	if r.StatusCode == 500 {
		return "", fmt.Errorf(pteErrorMsg + strings.Trim(string(respBody), "\""))
	}

	if r.StatusCode == 401 {
		errMsg := "Access Denied: Ensure the correct credentials are in /var/tmp/.tower-creds and the Foreman user has access to %v in AWX"
		newErrMsg := fmt.Sprintf("%v%v", pteErrorMsg, errMsg)
		return "", fmt.Errorf(newErrMsg)
	}

	if strings.Trim(string(respBody), "\"") == "successful" {
		return "successful", nil
	} else {
		return "", requestErr
	}
}

// Prelaunch does all the "pre-flight" checks necessary in order to kick off the ansible jobs
// it's necessary to split this up because midtier and edge can't kick off ansible jobs
func Prelaunch(fqdn string) (JobVars, error) {
	var jobVars JobVars

	// make sure the network is connected
	if err := CheckConnectivity(); err != nil {
		return jobVars, fmt.Errorf("prelaunch(): %w", err)
	}

	// read the env vars left by Foreman, read the AWX vars file on osmedia based on the distro and host environment
	// and based on those create and return an object with all the necessary info
	jobVars, jobErr := ReadAwxVars()
	if jobErr != nil {
		if strings.Contains(jobErr.Error(), "404") {
			return jobVars, jobErr
		}
		return jobVars, fmt.Errorf("prelaunch(): %w", jobErr)
	}

	// make the systemd journal persist after a reboot so we can check the final status
	// also just generally good to have
	if err := PersistentJournal(); err != nil {
		return jobVars, fmt.Errorf("prelaunch(): %w", err)
	}

	// is the FQDN resolvable?
	if err := DnsLookup(fqdn, foremanOptions.Mock); err != nil {
		//not a program issue, IP doesn't resolve correctly
		if strings.Contains(err.Error(), "DDNS") {
			return jobVars, err
		}
		return jobVars, fmt.Errorf("prelaunch(): %w", err)
	}

	return jobVars, nil
}

// ReadAwxVars reads the AWX job template names and IDs from a JSON formatted text file
func ReadAwxVars() (JobVars, error) {
	var jobVars JobVars
	var awxVarsResp *http.Response

	// reading our env vars set by Foreman
	foremanVars, err := ReadForemanVars()
	if err != nil {
		return jobVars, fmt.Errorf("getJobVars(): %w", err)
	}

	distro, err := CheckDistro()
	if err != nil {
		return jobVars, fmt.Errorf("ReadAwxVars(): %w", err)
	}

	awxVarsURL := fmt.Sprintf("%vawxclient-dev/awxvars/", foremanVars.Server)
	var awxVarsLocation string

	if distro == "centos" && foremanVars.Type == "internal" {
		awxVarsLocation = fmt.Sprintf("%v%v", awxVarsURL, "centos-internal.json")
	} else if distro == "rocky" && foremanVars.Type == "internal" {
		awxVarsLocation = fmt.Sprintf("%v%v", awxVarsURL, "rocky-internal.json")
	} else if distro == "centos" && foremanVars.Type == "midtier" {
		awxVarsLocation = fmt.Sprintf("%v%v", awxVarsURL, "centos-midtier.json")
	} else if distro == "rocky" && foremanVars.Type == "midtier" {
		awxVarsLocation = fmt.Sprintf("%v%v", awxVarsURL, "rocky-midtier.json")
	} else if distro == "centos" && foremanVars.Type == "edge" {
		awxVarsLocation = fmt.Sprintf("%v%v", awxVarsURL, "centos-edge.json")
	} else if distro == "rocky" && foremanVars.Type == "edge" {
		awxVarsLocation = fmt.Sprintf("%v%v", awxVarsURL, "rocky-edge.json")
	}

	var awxVars AwxVars

	if strings.Contains(foremanOptions.File, "http") || foremanOptions.File == "" {
		awxVarsResp, err = http.Get(awxVarsLocation)
		if err != nil {
			return jobVars, fmt.Errorf("ReadAwxVars(): http.Get(): %w", err)
		} else if awxVarsResp.StatusCode == 404 {
			return jobVars, fmt.Errorf("ERROR: 404: can't find %v", awxVarsLocation)
		} else if awxVarsResp.StatusCode != 200 {
			return jobVars, fmt.Errorf("ERROR: ReadAwxVars(): http.Get(): %v", awxVarsResp.Status)
		}
		awxVarsBody, err := io.ReadAll(awxVarsResp.Body)
		if err != nil {
			return jobVars, fmt.Errorf("client() io.ReadAll(): %w", err)
		}

		if jsonErr := json.Unmarshal(awxVarsBody, &awxVars); jsonErr != nil {
			return jobVars, fmt.Errorf("client(): json.Unmarshal(): %w", err)
		}
	} else if foremanOptions.File != "" {
		data, err := os.ReadFile(foremanOptions.File)
		if err != nil {
			return jobVars, fmt.Errorf("ReadAwxVars(): os.ReadFile(): %w", err)
		}
		json.Unmarshal(data, &awxVars)
	} else {
		return jobVars, fmt.Errorf("ReadAwxVars(): can't find awxvars")
	}

	jobVars.InvID = awxVars.InventoryID
	jobVars.InvName = awxVars.InventoryName
	jobVars.Reboot = awxVars.Reboot
	jobVars.BreakglassName = awxVars.BreakglassName
	jobVars.BreakglassID = awxVars.BreakglassID
	jobVars.BaselineName = awxVars.BaselineName
	jobVars.BaselineID = awxVars.BaselineID
	jobVars.DesiredRelease = fmt.Sprintf("%v.%v", foremanVars.OSmajor, foremanVars.OSminor)
	jobVars.FQDN = fqdn
	jobVars.Type = foremanVars.Type
	jobVars.Facility = foremanVars.Facility
	jobVars.Relay = foremanVars.Relay

	return jobVars, nil
}

// CheckConnectivity ensures the host has network connectivity
func CheckConnectivity() error {
	pinger, err := ping.NewPinger("osmedia.bamtech.co")
	if err != nil {
		return fmt.Errorf("checkConnectivity(): ping.NewPinger(): %w", err)
	}
	pinger.Count = 3
	pinger.Run()                 // blocks until finished
	stats := pinger.Statistics() // get send/receive/rtt stats
	if stats.PacketLoss > 10 {
		err := errors.New("error: high packet loss, check network connnectivity")
		return fmt.Errorf("checkConnectivity(): pinger.Statistics(): %w", err)
	}
	return nil
}

// PersistentJournal ensures that Systemd journal logs persist after a reboot
func PersistentJournal() error {
	file, err := os.ReadFile("/etc/systemd/journald.conf")

	if err != nil {
		return fmt.Errorf("persistentJournal(): os.ReadFile(): %w", err)
	}

	r1 := regexp.MustCompile(`(#Storage=[a-zA-Z]+)`)
	rx1 := r1.ReplaceAllString(string(file), "Storage=persistent")

	r2 := regexp.MustCompile(`(#SystemMaxUse=\w+)`)
	rx2 := r2.ReplaceAllString(rx1, "SystemMaxUse=500M")

	if err := os.WriteFile("/etc/systemd/journald.conf", []byte(rx2), 0644); err != nil {
		return fmt.Errorf("persistentJournal(): os.WriteFile(): %w", err)
	}
	return nil
}

// DnsLookup ensure that the FQDN set by Foreman matches the DNS A record
func DnsLookup(fqdn, mock string) error {

	// skipping DNS lookup if we're mocking a build since the lookup will fail
	if mock != "" {
		return nil
	}

	foremanVars, err := ReadForemanVars()
	if err != nil {
		return fmt.Errorf("dnsLookup(): %w", err)
	}

	ips, err := net.LookupIP(fqdn)
	if err != nil {
		return fmt.Errorf("dnsLookup(): net.LookupIP(): %w", err)
	}

	for _, value := range ips {
		if value.String() == foremanVars.BuildIP {
			return nil
		}
	}

	//IP doesn't resolve correctly
	msg1 := "DNS A record and IP set by Foreman don't match, ensure host was registered using the DDNS tool"
	return fmt.Errorf(msg1)

}

// ReadForemanVars reads the environment file in /etc left by Foreman after a successful provisioning
func ReadForemanVars() (ForemanVars, error) {
	var envFile string
	var foremanVars ForemanVars

	// check which env file exists if any
	if _, err := os.Stat("/etc/dss.env"); !errors.Is(err, os.ErrNotExist) {
		envFile = "/etc/dss.env"
	}
	if _, err := os.Stat("/etc/bam.env"); !errors.Is(err, os.ErrNotExist) {
		envFile = "/etc/bam.env"
	}
	if envFile == "" {
		err := fmt.Errorf("readForemanVars(): can't find environment file containing Foreman variables")
		return foremanVars, err
	}

	// read the file
	vars, err := ReadFile(envFile)
	if err != nil {
		return foremanVars, fmt.Errorf("ReadForemanVars(): %w", err)
	}

	for key, value := range vars {
		switch key {
		case "build_server":
			foremanVars.Server = value
		case "facility":
			foremanVars.Facility = value
		case "type":
			foremanVars.Type = value
		case "buildip":
			foremanVars.BuildIP = value
		case "osmajor":
			foremanVars.OSmajor = value
		case "osminor":
			foremanVars.OSminor = value
		case "mtrelay":
			foremanVars.Relay = value
		}
	}

	if foremanVars.Server == "" {
		return foremanVars, fmt.Errorf("ReadForemanVars(): foremanVars.Server key is empty. Ensure %v contains the correct data", envFile)
	} else if foremanVars.Facility == "" {
		return foremanVars, fmt.Errorf("ReadForemanVars(): foremanVars.Facility key is empty. Ensure %v contains the correct data", envFile)
	} else if foremanVars.Type == "" {
		return foremanVars, fmt.Errorf("ReadForemanVars(): foremanVars.Type key is empty. Ensure %v contains the correct data", envFile)
	} else if foremanVars.BuildIP == "" {
		return foremanVars, fmt.Errorf("ReadForemanVars(): foremanVars.BuildIP key is empty. Ensure %v contains the correct data", envFile)
	} else if foremanVars.OSmajor == "" {
		return foremanVars, fmt.Errorf("ReadForemanVars(): foremanVars.OSmajor key is empty. Ensure %v contains the correct data", envFile)
	} else if foremanVars.OSminor == "" {
		return foremanVars, fmt.Errorf("ReadForemanVars(): foremanVars.OSminor key is empty. Ensure %v contains the correct data", envFile)
	}

	return foremanVars, nil
}

// KickoffJobs launches breakglass and baseline apply
func KickoffJobs(fqdn string, jobVars JobVars, mock string) (string, error) {
	var err error

	if awx, err = AwxClientSetup(); err != nil {
		return "", err
	}

	// hack to make sure our host exists in the required inventory
	if err := DoesHostExist(fqdn, jobVars); err != nil {
		if strings.Contains(err.Error(), "ERROR") {
			return "", err
		}
		return "", fmt.Errorf("kickoffJobs(): %w", err)
	}

	// skip launching jobs in order to test other functions quickly
	if mock != "" {
		PrintStatus(fmt.Sprintf("INFO: Kicking off %v...", jobVars.BaselineName))
		PrintStatus(fmt.Sprintf("INFO: Kicking off %v...", jobVars.BreakglassName))
		return "successful", nil
	} else {
		// kicking off breakglass
		breakglassParams := map[string]interface{}{"inventory": jobVars.InvID, "limit": jobVars.FQDN}
		err := LaunchJob(fqdn, jobVars.BreakglassName, jobVars.BreakglassID, breakglassParams)
		if err != nil {
			if strings.Contains(err.Error(), "failed") {
				return "", err
			}
			return "", fmt.Errorf("KickoffJobs(): %w", err)
		}

		// kicking off baseline
		baselineParams := map[string]interface{}{"inventory": jobVars.InvID, "limit": jobVars.FQDN,
			"extra_vars": fmt.Sprintf("{desired_release: %v, reboot: false}", jobVars.DesiredRelease),
		}
		err = LaunchJob(fqdn, jobVars.BaselineName, jobVars.BaselineID, baselineParams)
		if err != nil {
			if strings.Contains(err.Error(), "failed") {
				return "", err
			}
			return "", fmt.Errorf("KickoffJobs(): %w", err)
		}
	}
	return "successful", nil
}

// CleanUp removes the AWX credentials file, disables the awxclient systemd unit, and optionally reboots the host upon completion
func CleanUp(reboot, mock string) error {
	PrintStatus("INFO: Cleaning up...")

	// if we're mocking a job launch, don't clean up because we're testing stuff
	if mock != "" {
		PrintStatus("INFO: Build completed successfully. Please manually reboot.")
		os.Exit(0)
	}

	// if /var/tmp/.tower_creds exists, remove it
	if _, err := os.Stat("/var/tmp/.tower_creds"); err == nil {
		if err := os.Remove("/var/tmp/.tower_creds"); err != nil {
			return fmt.Errorf("CleanUp(): os.Remove(): %w", err)
		}
	}

	// figuring out which unit is enabled
	unitfile, err := filepath.Glob("/etc/systemd/system/multi-user.target.wants/awxclient*")
	if err != nil {
		return fmt.Errorf("CleanUp(): filepath.Glob(): %w", err)
	} else if len(unitfile) < 1 {
		return fmt.Errorf("CleanUp(): Can't find Systemd Unit")
	}

	// disabling the unit so it won't attempt to kick off the jobs after a reboot
	_, execErr := exec.Command("systemctl", "disable", unitfile[0]).Output()
	if execErr != nil {
		return fmt.Errorf("CleanUp(): exec.Command().Output(): %w", err)
	}

	// removing the package
	_, execErr = exec.Command("/usr/bin/yum", "-y", "remove", "awxclient").Output()
	if execErr != nil {
		return fmt.Errorf("CleanUp(): exec.Command().Output(): %w", err)
	}

	if reboot == "true" {
		PrintStatus("INFO: Build completed successfully. Rebooting in 60 seconds.")
		time.Sleep(1 * time.Minute)
		syscall.Sync()
		syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART)
	} else {
		PrintStatus("INFO: Build completed successfully. Please manually reboot.")
	}

	return nil
}
