FROM centos:8

ENV GOLANG_VERSION "1.17.2"

RUN yum update -y && yum install -y epel-release dnf-plugins-core
RUN yum -y groupinstall "Development Tools"
RUN curl -L https://dl.google.com/go/go${GOLANG_VERSION}.linux-amd64.tar.gz | tar -C /usr/local -zxvf -

ENV GOPATH /go
ENV PATH $GOPATH/bin:/usr/local/go/bin:$PATH
RUN mkdir -p "$GOPATH/src" "$GOPATH/bin" && chmod -R 777 "$GOPATH"

WORKDIR /go/src/github.com/stackrox/scanner
