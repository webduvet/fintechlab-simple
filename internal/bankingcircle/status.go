package bankingcircle

// PaymentStatus maps a payment's state onto Banking Circle's PaymentStatus
// enum, as GET /api/v1/payments/singles/{payment-id}/status reports it.
//
// This is the read a reconciliation sweep falls back to for a payment the
// intraday report does not show as processed — in particular a rejection,
// which that report never carries and the rejection report cannot name by
// paymentId.
func PaymentStatus(s NotificationType) string {
	switch s {
	case NotificationOutgoingPaymentProcessed, NotificationIncomingPaymentProcessed:
		return "Processed"
	case NotificationOutgoingPaymentRejected:
		return "Rejected"
	case NotificationMissingFunding:
		return "MissingFunding"
	case NotificationReversed:
		return "Reversed"
	}
	// Booked, routing, direct-debit pending: accepted, not yet finished.
	return "PendingProcessing"
}
