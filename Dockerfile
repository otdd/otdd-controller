FROM centos:7
COPY ./otdd-controller /usr/local/bin/
WORKDIR /usr/local/bin/
CMD ["/usr/local/bin/otdd-controller"]
