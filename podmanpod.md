antoher improvement could be - to make a pod out of podman containers needed to run this test bed.

we do have two separate things - we run test bed
and we run the application in test which also could be the container - so I am talking about the test bed, not the application in test.


test bed is having well defined api and ports simulating the real environment so the applicaiton in test can be run with the set of envirnment variables which would point to the test bed rather than real application.


not sure how userful it would be but perhaps we can distribute it as podman pod with the configuration yaml file so we don't neeed to instruct the users to run make, terminals etc.

we would jut run podman command to run the entire pod with some config file and podman will manage the lifecycle of it.



=====================

I want to simplify things.
I think the initial feature implementation - the banking-circle intraday reconcile went bad. Initially instructed that the reconcile sweep has precendence over the webhook. Which was a bit of misunderstanding from my side. The webhooks are equal source of truth as the reconcile sweep. if any is different it would be somehting we need to shout loud.

there is sime sequence of possible states  of payment lifecycle from the BC point view.
payment
    booked
    processing
    processed | rejected 


we perhaps should create a sequence - like if any webhook or sweep will arrive with earlier state than the payment is already set it will not be processed - we need logged. payment status change to applicable. payment lifecycle is already forward. e.g. webook delivers processed and sweep delivers processing. (sweep might take longer to evaluate on our side and webhook meantime could arrive) than processing is already behind processed so it won't be applied. conradicting states should not happen and indicates serious system malfunction.

so when the sweep is evaluated against or pending transactions it will check if the sweep information is not behind the transaction state.

similar logic needs to be reflected on webhook handling. if the transaction status is already ahead of webhook information it will not be applied. contradicting statuses need to shout loud.

Another issues I found is check for return property of the transactions of the intraday recon.
this was misunderstanding of implementing agent.
we might get info about all sort of payment - we do evaluate only or transaction relevant to stage rootId. and only the transaction in pending state. any other transaction is already ahead. This actually solved the issue above re. the checking he transaction DB status. 
So we pull transacitons relative to relevant rootID. we filter for bcPaymentID - these are the transactions we need to verify against the intraday report.
we iterate and act on it.

we should not overcomplicated and over engineer this.

you can spin up verification or review agent ensuring the correctness of the solution

=====================


simlab = our simulated fintech lab environemnt
harness = infinite-local-test harness to run infinite platform (settle) against the simlab

Tidying up the test bed

I had hard time to spin up the simlab agains the environment.
the finlab contains certs in their location and the test harness needs to pull the keys from the location

There is lack of clarity around how to start the harness - especially around settle-processing lambda
also it is unclear if I need to spin up app/banking-circle (not simlab) in the container or via nx, the same with gateway.
also it is a bit unclear where are the environment variables coming from if I run podman compose up in the harness comparing when the apps are stared via init/scripts which clearly reaches out to nx

I am not saying the solution is wrong, it does work, we perhaps need so clarity how to start it from scratch.
one alternative how to get certs and keys - the simlab upon the start will generate them and make a download button for all certs including the instruction where to put them
also once the simlab is up running - it would be handy if we could point and set the variables pinting to app in testing.
e.g. app/banking-circle might be running on port 3014 and also 3114 via nx. perhaps we could dynamically swap it.

anothe improvement could be - some services we might not need up and running. 
thsi could be done two ways
1. yaml config file - preset with mandatory services and additional
2. we already have configuration page - we could eventually have option to start and stop service there. e.g. stand in webhook listener might be usefull for verfication, but ultimately it is not need for testing and development.


=====================

minor UI improvement
we could call it finlab v1 as the v2 version will be or might be besed on reusable pods and kernels which needs some refinement.


in the diagram - we could click on the arrow and it could open sidebar and show live traffic flowing through that arrow. if we select 2 they will be stacked. perhaps we could have it on the right side. perhaps even retractable side bar.
