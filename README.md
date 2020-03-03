# otdd-controller

The otdd controller is a kubernets controller that acts for otdd resources changes.

- *Redirector Creating*. Creates a redirector pod for the target deployment. The redirector reserves all the labels of the target deployment and acts like just another normal pod processing requests excepting it randomly redirect the requests to the recorder pod at an interval( defaults to 1s).


- *Recorder Creating*. Creates a recorder pod for the target deployment. The recorder removes all the labels of the target deployment so no requests will reach it except the redirector. The recorder then records the inbound request/response and its corresponding outbound requests/responses and send them to otdd server through mixer.
