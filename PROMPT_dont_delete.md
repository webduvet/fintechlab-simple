I think this repo needs a  litte refinement.

Here is the flow what we are trying to simulate:

Our platform is called infinitepay and is not part of this repo - this repo is about simulating the real world data to facilitate smooth local development and testing. This test harness is also foundation for fintech sim lab - my own project - which is the set of test harnesses, mocked services, tutorials and recepies how to do stuff.

Infinitepay is while label payment facilitator. we will have distributers, partners, customers (merchants) merchants can have mulitple outlets. Worldline (3rd party fintec company, we use as acquirer) uses the word submerchant and it indentifier they use is MID. That is not that important for this, just to have some context.
Intinitepay onboards the merchants and offer them payment products via various gateways with attached fees. At the moment we have one acquirer (worldline) but in the future we might have more.

The best yould be if I describe the flow where I can outline what data is coming to our platform and from where.

I try to describe the money flow. follow the money they say. And I will focus on interaction with 3rd party system as this project is about simulating. Many parts we process on our side I intentionally leave out.

customers pay at merchants paypoint via some gateway we provide. this means the money from visa ms amex etc. will end up in the acquirer account.
The next day acquirer (worldline) will send lump som (for all associated merchants) to our safeguarding account. In order for us to be able to track each payment and attach fees and also for audit purposes they provide daily settlement file which is in our case in CSV format [worldline docs]:(https://docs.acquiring.worldline-solutions.com/api-reference). we recieve the file via SFTP between 8 and 10 in the morning (morning file) and we receive confirmation file (afternoon file). 
Our system reaches out to worldline provided SFTP location (details found on docs)
we need to simulate the worldline api we interact with  and sftp service - we want the very same requirement for pgp encrtyption and any authentication should be indentical. I want to be able in UAT environment or production environemnt just to change the env variabl pointing to real service. the simualted version should adhere to the same api as the real one. 

when the file is there we pull it and process it (our black box with internal virtual accounting ledger system)
we process how much we need to send to each merchant outlet (we calculate per outlet - remember refered as MID in worldline settlement file)
once it is done we need to issue payment via b4bpayment - moving money from our SGA to merchants account
b4b invokes callback with regulatory checks status of involeved payment parties - these are the kind of thing we try to simulate here.
if all are pass - b4bpayments hands over the rest of the payment lifecycle to banking circle.
(https://b4bpayments.readme.io/reference/createcompany)
we we want to simulate the service which receives the payment, and runs the regulatory checks - obviously these are mocked and return the success. - it invokes the given callback - exactly as per documentation in b4bpayments docs pages. 
the service should also communicate with banking circle service - the communication between them is not known - nevertheless - once all regulatory checks in oversight pass the payment is handed over to banking circle - we can assume some internal endpoint. Once banking circle recives this it will process the payment.

the communication with banking circle is via webhooks
I do have docs (https://docs.bankingcircleconnect.com/docs/receiving-webhooks) and here
https://docs.bankingcircleconnect.com/reference/post_api-v1-notificationselfservice-subscriptionevent
https://docs.bankingcircleconnect.com/reference/get_api-v1-notificationselfservice-subscription
banking circle sends notification in batches - so in our simulated environment - we want the same - if you notice when creating/updating subscription we can pick the batch size - this we will use in simulation. for the first iteration all payments can be successful. and the information is sent in batch. also it needs that exponencial backoff - when starting banking circle - I want to have some config file which we would be able to setup parameters like this - so we don't need to wait 2 days for it to unsubscribe.
look up docs or my own notes (avram) mentioned below.

we do have ecs running app listening to banking circle event notification - we subscribe, manage subscription and listen to events - all these should be part of simulation
we don't use mTLS, but perhaps imulation should offer it.

we receive the notification and we complete the transation in our virtual ledger
we report and distribute emails

so essentially - we communicate with 3 services - we receive the settlement file, we make payments, we receive status about the payments, 


this repo started the work - but I am not very confident if it is capable to simulate all the above
your job is to verify  and suggest improvements. 
than implement improvements and make sure it is running. 

The simulation also includes sending payments to our SGA - we do not interact directly, but it is good to have. I specifically want you to focus on worldline, b4bpayment oversight and bankingcircleconnet. 

avram - my own notes about this
https://github.com/webduvet/avram/tree/main/docs/fintech/banking-circle
also accessible on ../avram locally - there is a lot of information worth checking ouit

b4b and banking circle but documentation behind simple password to keep bots out:
https://b4bpayments.readme.io/reference
b4b docs:
Welcome2B4BPayments

https://docs.bankingcircleconnect.com/docs
banking-circle docs:
CJHBCdocs!45IVA%



sumarize if you understood the task well. refine if not, ask question at the beginning. once we agree on plan you should be able to work independently on improvements
