Name:           awxclient-dev
Version:        2.0.0
Release:        0
Summary:        A tool to kickoff Breakglass and Baseline Apply jobs in all environments
License:        GPL
Packager:       Brandon Golway
BuildRoot:      /root/rpmbuild/

%description
dssfinish.sh, mtfinish.sh, and mtrelay.php along with a webserver all in one compact binary.

%build
cd /root/awxclient
go build

%install
mkdir -p $RPM_BUILD_ROOT/usr/bin
mkdir -p $RPM_BUILD_ROOT/usr/lib/systemd/system/
cp /root/awxclient/awxclient $RPM_BUILD_ROOT/usr/bin
cp /root/awxclient/systemd/*.service $RPM_BUILD_ROOT/usr/lib/systemd/system/

%files
/usr/bin/awxclient
/usr/lib/systemd/system/awx-relay.service
/usr/lib/systemd/system/awxclient.service

%clean
rm -rf $RPM_BUILD_ROOT

%changelog
* Tue Feb 14 2023 Brandon Golway <brandon.golway@disneystreaming.com>
- Rewrote main.go which simplifies execution and now only requires two systemd units instead of seven
- Combined internal.go and client.go into foreman.go, and moved some functions over to shared.go in preparation for adding HAL functionality
- Which version of breakglasss and baseline to launch are now decided based upon parsed Foreman env vars and if /etc/rocky-release exists
- Relay FQDN is now parsed from the MTRELAY Foreman env var
- Which osmedia server to use is now read from the BUILD_SERVER Foreman env var
* Thu Feb 9 2023 Brandon Golway <brandon.golway@disneystreaming.com>
- Fixed DnsLookup() so it attempts to match against all reported IPs, not just the first one
- Fixed error formatting when there is an error reported that isn't a program error (ex. DNS A record and Foreman set IP don't match)
* Thu Feb 2 2023 Brandon Golway <brandon.golway@disneystreaming.com>
- Made awx object global instead of passing it around a bunch of times
- Cleaned up KickoffJobs() and rewrote LaunchJob() in order to make it more simplistic
- Added code to prevent additional executions of a successful job execution
- Fixed 'error: successful' in midtier build and other error messages
- Fixed variable types in prelaunch.go
* Wed Feb 1 2023 Brandon Golway <brandon.golway@disneystreaming.com>
- Fixed a bug I introduced in GetGroupID() in the last release
* Tue Jan 31 2023 Brandon Golway <brandon.golway@disneystreaming.com>
- Fixed bug with GetGroupID() when host is in two inventories
* Mon Jan 30 2023 Brandon Golway <brandon.golway@disneystreaming.com>
- Fixed HTTP error responses
- Forked awx-go in order to shorten HTTP responses from AWX
* Tue Jan 24 2023 Brandon Golway <brandon.golway@disneystreaming.com>
- Overhauled logging for awx-relay. 
- All awx-relay related logs are written to the awx-relay.service journal, all other logs are written to /var/log/awx-relay/$FQDN.log
- Other general logging/status output improvements
* Thu Jan 19 2023 Brandon Golway <brandon.golway@disneystreaming.com>
- Modified DnsLookup() so that it works with mocked builds
- Fixed Foreman env vars file detection in ReadForemanVars() 
* Thu Jan 19 2023 Brandon Golway <brandon.golway@disneystreaming.com>
- Fixed CleanUp() so that it uninstalls the RPM and disables the correct systemd unit
* Thu Dec 29 2022 Brandon Golway <brandon.golway@disneystreaming.com>
- Initial release
