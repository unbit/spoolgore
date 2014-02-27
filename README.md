spoolgore
=========

A simple mail "spool and send" daemon written in Go


```sh
spoolgore --smtpaddr example.com:25 /var/spool/yourapp
```

Why ?
=====

Sending e-mails from your app via remote smtp can be a huge problem: if your smtp service is slow, your app will be slow, if your service is blocked your app will be blocked. If you need to send a gazillion email in one round your app could be blocked for ages.

"spool and send" daemons allow you to store the email as a simple file on a directory (the 'spool' directory), while a background daemon will send it as soon as possible, taking care to retry on any error.

Project like nullmailer (http://untroubled.org/nullmailer/) work well, but they are somewhat limited (in the nullmailer case having multiple instances running on the system requires patching).

Spoolgore tries to address the problem in the easiest possible way.

Why Go ?
========

MTA (and similar) tend to spawn an additional process for each SMTP transaction. This is good (and holy) for privileges separation, but spoolgore is meant to be run by each user (or single application stack) so we do not need this kind of isolation in SMTP transactions.

Go (thanks to goroutines) allows us to enqueue hundreds of SMTP transactions at the cost of few KB of memory.

Obviously we could have written it in python/gevent or perl/coro::anyevent or whatever non-blocking coroutine/based technology you like, but we choose go as we wanted to give a try to it (yes, no other reasons)
