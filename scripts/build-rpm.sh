#!/bin/bash

RPMBUILD="/root/rpmbuild/"
SPECFILE="$RPMBUILD/SPECS/awxclient.spec"
AWXCLIENT="/root/awxclient"

#Ensure the SPECFILE exists in $RPMBUILD
if [[ ! -a $SPECFILE ]];then
    ln -s $AWXCLIENT/awxclient.spec $SPECFILE
    if [[ $? -gt 0 ]];then
	echo "Couldn't find awxclient.spec"
	exit 2
    fi
fi

#Pull down any new changes
cd $AWXCLIENT
git pull

#Edit and commit the SPECFILE
vim $SPECFILE
VERSION=$(grep -E 'Version' awxclient.spec|awk '{print $2}')
RELEASE=$(grep -E 'Release' awxclient.spec|awk '{print $2}')
git add awxclient.spec && git commit -m "v$VERSION-$RELEASE" && git push

#Clean out old RPM and build a new binary only RPM from the SPECFILE
rm -f $RPMBUILD/RPMS/x86_64/*
rpmbuild --quiet -bb $SPECFILE

#If RPM build failed, exit
if [[ $? -gt 0 ]];then
    exit 2
fi

