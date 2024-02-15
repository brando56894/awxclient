##Systemd Unit Files

These unit files are enabled by Foreman after a successful build of a host. They are included in the RPM and the respective unit file is enabled depending on which template it was built with in Foreman. This is defined in the template's kickstart file. Once started it pulls down the respective awx ini file from http://osmedia.bamtech.co/awxclient/awxvars to /var/tmp and uses it to kick off the respective jobs for that OS and host type. If the post-build was successful, awxclient removes the unit.
