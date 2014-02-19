package main

import (
        "flag"
        "time"
        "log"
        "io/ioutil"
	"net/smtp"
	"net/mail"
	"strings"
	"os"
	"path"
	"path/filepath"
)

type Config struct {
	SpoolDir string
	SmtpAddr string
	SmtpUser string
	SmtpPassword string
	Freq int
	MaxAttempts int
}

var config Config

/*
	0 -> just pushed
	1 -> still in progress
	2 -> sent
*/

type SentStatus struct {
	Address string
	Status int
	Attempts int
	NextAttempt time.Time
}

type MailStatus struct {
	From string
	To []SentStatus
	Cc []SentStatus
	Bcc []SentStatus
	Attempts int
}

var status map[string]MailStatus

func parse_options() {
	flag.StringVar(&config.SmtpAddr, "smtpaddr", "127.0.0.1:25", "address of the smtp address to use in the addr:port format")
	flag.IntVar(&config.Freq, "freq", 10, "frequency of spool directory scans")
	flag.IntVar(&config.MaxAttempts, "attempts", 100, "max attempts for failed SMTP transactions before giving up")
	flag.Parse()
	if flag.NArg() < 1 {
		log.Fatal("please specify a spool directory")
	}
	config.SpoolDir = flag.Args()[0]
}

func send_mail(ss *SentStatus, file string, from string, to string, msg *[]byte) {
	log.Println(file,"sending mail to", to, "attempt:", ss.Attempts)
	dest := []string{to}
	err := smtp.SendMail(config.SmtpAddr, nil, from, dest, *msg)
	if err != nil {
		log.Println(file,"SMTP error, mail to", to, err)
		ss.Status = 0
		ss.Attempts++
		if ss.Attempts >= config.MaxAttempts {
			log.Println(file, "max SMTP attempts reached for",to, "... giving up")
			ss.Status = 2
		}
		if (ss.Attempts > 30) {
			ss.NextAttempt = ss.NextAttempt.Add(time.Duration(30) * time.Duration(60) * time.Second)
		} else {
			ss.NextAttempt = ss.NextAttempt.Add(time.Duration(ss.Attempts) * time.Duration(60) * time.Second)
		}
		return
	}
	ss.Status = 2
	log.Println(file, "successfully sent to", to)
}

func try_again(file string, msg *mail.Message) {
	in_progress := false

	mail_status := status[file]

	// rebuild message (strip Bcc)

	// manage To
	for i, send_status := range mail_status.To {
		s := send_status.Status
		switch s {
			case 0:
				in_progress = true
				if send_status.NextAttempt.Equal(time.Now()) == true || send_status.NextAttempt.Before(time.Now()) == true {
					// do not use send_status here !!!
					mail_status.To[i].Status = 1
					body := []byte(string("hello"))
					go send_mail(&mail_status.To[i], file, mail_status.From, send_status.Address, &body)
				}
			case 1:
				in_progress = true
		}
	}

	// manage Cc
	for i, send_status := range mail_status.Cc {
		s := send_status.Status
		switch s {
			case 0:
				in_progress = true
				if send_status.NextAttempt.Equal(time.Now()) == true || send_status.NextAttempt.Before(time.Now()) == true {
					// do not use send_status here !!!
					mail_status.Cc[i].Status = 1
					body := []byte(string("hello"))
					go send_mail(&mail_status.Cc[i], file, mail_status.From, send_status.Address, &body)
				}
			case 1:
				in_progress = true
		}
	}

	// manage Bcc
	for i, send_status := range mail_status.Bcc {
		s := send_status.Status
		switch s {
			case 0:
				in_progress = true
				if send_status.NextAttempt.Equal(time.Now()) == true || send_status.NextAttempt.Before(time.Now()) == true {
					// do not use send_status here !!!
					mail_status.Bcc[i].Status = 1
					body := []byte(string("hello"))
					go send_mail(&mail_status.Bcc[i], file, mail_status.From, send_status.Address, &body)
				}
			case 1:
				in_progress = true
		}
	}



	if in_progress == false {
		// first we try to remove the file, on error we avoid to respool the file
		err := os.Remove(file)
		if err != nil {
			log.Println(file,"unable to remove mail file,", err)
			return
		}
		// ok we can now remove the item from the status
		delete(status, file)
	}
}

func parse_mail(file string) {
	f, err := os.Open(file)
	if err != nil {
		log.Println(file,"unable to open mail file,", err)
		return
	}
	defer f.Close()
	msg, err := mail.ReadMessage(f)
	if err != nil {
		log.Println(file,"unable to parse mail file,", err)
		return
	}

	mail_status := MailStatus{}
	mail_status.To = make([]SentStatus, 0)
	mail_status.Cc = make([]SentStatus, 0)
	mail_status.Bcc = make([]SentStatus, 0)

	if _,ok := msg.Header["To"]; ok {
		to_addresses, err := msg.Header.AddressList("To")
		if err != nil {
			log.Println(file,"unable to parse mail \"To\" header,", err)
			return
		}
		for _,addr := range to_addresses {
			mail_status.To = append(mail_status.To, SentStatus{Address: addr.Address, Status:0, NextAttempt: time.Now()})
		}
	}

	if _,ok := msg.Header["Cc"]; ok {
		cc_addresses, err := msg.Header.AddressList("Cc")
		if err != nil {
			log.Println(file,"unable to parse mail \"Cc\" header,", err)
			return
		}
		for _,addr := range cc_addresses {
			mail_status.Cc = append(mail_status.Cc, SentStatus{Address: addr.Address, Status:0, NextAttempt: time.Now()})
		}
	}

	if _,ok := msg.Header["Bcc"]; ok {
		bcc_addresses, err := msg.Header.AddressList("Bcc")
		if err != nil {
			log.Println(file,"unable to parse mail \"Bcc\" header,", err)
			return
		}
		for _,addr := range bcc_addresses {
			mail_status.Bcc = append(mail_status.Bcc, SentStatus{Address: addr.Address, Status:0, NextAttempt: time.Now()})
		}
	}

	// is the mail already collected ?
	if _,ok := status[file]; ok {
		try_again(file, msg)
		return
	}

	status[file] = mail_status
	try_again(file, msg)
}

func scan_spooldir(dir string) {
	d, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Println("unable to access spool directory,", err)
		return
	}
	for _, entry := range d {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		abs,err := filepath.Abs(path.Join(config.SpoolDir, entry.Name()))
		if err != nil {
			log.Println("unable to get absolute path,", err)
			continue
		}
		parse_mail(path.Clean(abs))
	}
}

func main() {
	parse_options()
	log.Println("--- starting spoolgore on directory", config.SpoolDir, "---")
	status = make(map[string]MailStatus)
	timer := time.NewTimer(time.Second * time.Duration(config.Freq))
	for {
		select {
			case <- timer.C:
				scan_spooldir(config.SpoolDir)
				timer.Reset(time.Second * time.Duration(config.Freq))
		}
	}
}
