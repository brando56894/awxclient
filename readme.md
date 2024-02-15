# dssfinish.sh, mtfinish.sh, and mtrelay.php along with a webserver all in one compact binary.

**awx-client.go**: Triggers Prelaunch() and collects data about the host in order to launch the correct AWX templates and then sends it off as a JSON object to the AWX relay webserver for processing.

**awx-relay.go**: Launches the AWX relay webserver and handles incoming requests from *awx-client.go*, the received information is then passed to *KickoffJobs()*.

**internal-build.go**: Performs all the necessary pre-checks, collects data, kicks off the necessary jobs, checks the status of each job as it's running, and upon success of the baseline job, cleans up after itself and optionally reboots the host. 

**launchjobs.go**: As the name implies, this file contains all the code necessary to launch AWX templates, such as
 * checking to make sure the host exists in the desired inventory, if it doesn't exist it is added to the correct inventory and associated with the relevant group
 * launching the relevant breakglass and baseline job templates from the passed in IDs
 * checking the status of the running breakglass and baseline jobs
 * cleaning up after a successful post-build 

**main.go**: This is pretty obvious, it's used to display the program options and launch the desired subcommand. 

**prelaunch.go**: Contains all the necessary host-side functions such as 
 * checking to make sure the host has connectivity
 * ensuring that the DNS A record matches what was set in Foreman
 * checking for the existance of /var/tmp/.tower_creds and logging into AWX
 * reading the env vars left by Foreman
 * setting the systemd journal as *persistent*
 * parsing passed in AWX job template info such as breakglass/baseline template name and ID, inventory name and ID. This data can be fed from a file or via CLI flags.

**shared.go**: contains functions that are shared between the main two functions: *Prelaunch()* and *KickoffJobs()*


