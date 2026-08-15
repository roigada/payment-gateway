# Payment Gateway

This context models a payment gateway between an e-commerce order service and a mock bank.

## Language

**Payment**:
The gateway-owned record of an attempt to collect money for an order through the mock bank.
_Avoid_: Transaction, charge, payment request

**Payment ID**:
The stable gateway-owned identity of a **Payment**. It uses the `pay_<uuid>` form and is distinct from the **Order ID** and from any identity returned by the **Mock Bank**.
_Avoid_: Transaction ID, order ID, bank ID

**Pending**:
A **Payment** whose authorization outcome is not yet known because the **Mock Bank** has not produced a definitive approval or decline.
_Avoid_: Failed, processing

**Aging Pending Payment**:
A **Pending** **Payment** that has remained unresolved long enough to require operational inspection.
_Avoid_: Stuck payment, failed payment, abandoned payment

**Payment Command Timeout**:
The API outcome when a synchronous payment command does not produce a final gateway response before its deadline. It does not mean that the **Payment** failed or that the **Mock Bank** did not complete the requested operation; the caller retries with the same **Idempotency Key** to recover the outcome.
_Avoid_: Failed payment, bank timeout

**Authorized**:
A **Payment** whose full **Amount** has been reserved by the **Mock Bank** and can still be captured or voided.
_Avoid_: Approved, held

**Expired**:
A **Payment** whose authorization was approved by the **Mock Bank** and for which the **Mock Bank** has definitively confirmed that the authorization can no longer be captured or voided because its hold expired. The stored **Authorization Expiration Time** predicts this outcome but does not itself transition the **Payment** to Expired.
_Avoid_: Timed out, stale, failed

**Declined**:
A **Payment** whose authorization was refused by the **Mock Bank**.
_Avoid_: Rejected, failed

**Decline Reason**:
A gateway-owned explanation for a **Declined** **Payment**, such as insufficient funds or an expired card.
_Avoid_: Bank error, failure reason

**Captured**:
A **Payment** whose full authorized **Amount** has been collected.
_Avoid_: Settled, charged

**Voided**:
A **Payment** whose authorization was cancelled before capture.
_Avoid_: Cancelled, deleted

**Refunded**:
A **Payment** whose full captured **Amount** has been returned.
_Avoid_: Reversed, reimbursed

**Payment Status**:
The current lifecycle position of a **Payment**.
_Avoid_: State

**Payment Status Conflict**:
A command outcome where the requested payment operation is valid in shape but not allowed by the current **Payment Status**.
_Avoid_: Invalid status conflict, transition error

**Bank State Conflict**:
A command outcome where the **Mock Bank** definitively disagrees with bank references or the **Amount** stored for a **Payment**. It is not a caller input error.
_Avoid_: Invalid input, bank failure, payment status conflict

**Order ID**:
The identity of the order that a **Payment** belongs to. The payment gateway treats it as an external reference owned by the order service.
_Avoid_: Order, purchase ID

**Order Service**:
The external service that owns Orders and is the gateway's authenticated caller.
_Avoid_: User, customer, merchant

**Service Principal**:
The authenticated identity of an external service calling the payment gateway.
_Avoid_: User, account, tenant

**Service Credential**:
An opaque secret that proves a **Service Principal** is the Order Service when calling the payment gateway.
_Avoid_: User token, session, password

**Payment Scope**:
A permission granted to a **Service Principal** to read or change Payments.
_Avoid_: Role, customer authorization, bank permission

**Customer ID**:
The identity of the customer whose mock bank account funds a **Payment**. The payment gateway treats it as an external reference.
_Avoid_: User ID, account ID

**Amount**:
The US-dollar value of a **Payment**, expressed in cents. A **Payment** has exactly one **Amount**.
_Avoid_: Price, total, money

**Currency**:
The monetary unit for a **Payment**. The payment gateway only uses US dollars.
_Avoid_: Currency code, settlement currency

**Card Details**:
The card number, CVV, and expiry values supplied to authorize or retry authorization for a **Payment** with the **Mock Bank**.
_Avoid_: Payment token, card account

**Mock Bank**:
The single fictional bank that approves, declines, captures, voids, and refunds payments.
_Avoid_: Card network, issuer, acquirer

**Bank Authorization ID**:
An identity assigned by the **Mock Bank** to an approved authorization. A Bank Authorization ID is used to continue authorization-related communication with the **Mock Bank**, but it is not a **Payment ID**.
_Avoid_: Bank Reference, Payment ID, public ID

**Authorization Expiration Time**:
The moment after which an approved authorization for a **Payment** can no longer be captured or voided.
_Avoid_: Timeout, deadline, stale time

**Authorize**:
To ask the **Mock Bank** to reserve the full **Amount** for a **Payment**.
_Avoid_: Charge, pay

**Authorization Retry**:
An attempt to resolve a **Pending** **Payment** by asking the **Mock Bank** again for the authorization outcome.
_Avoid_: Recreate payment, duplicate authorization

**Authorization Request Fingerprint**:
A non-reversible value used with a caller-provided **Idempotency Key** to check whether repeated authorization requests contain the same request values, including the **Amount**.
_Avoid_: Authorization Card Fingerprint, Bank Operation Key, Request ID

**Authorization Card Fingerprint**:
A non-reversible value used to check whether an **Authorization Retry** uses the same card number and expiry as the original authorization request for an existing **Payment ID**. The **Payment** already owns the **Amount**, so this value does not include the **Amount** and does not represent the card CVV.
_Avoid_: Authorization Request Fingerprint, Stored card, card token

**Capture**:
To collect the full authorized **Amount** for a **Payment**.
_Avoid_: Settle, complete

**Void**:
To cancel an authorized **Payment** before it is captured.
_Avoid_: Cancel, delete

**Refund**:
To return the full captured **Amount** for a **Payment**.
_Avoid_: Reimburse, reverse

**Idempotency Key**:
A caller-provided opaque operation identity used to make retried payment operations produce one result. The gateway requires it to be non-empty but does not interpret its format.
_Avoid_: Request ID, correlation ID

**Idempotency Replay**:
The gateway response to a repeated payment command that uses the same **Idempotency Key** and the same request values as an already completed command.
_Avoid_: Duplicate operation, cached request

**Idempotency Replay Window**:
The 24-hour period after a payment command completes during which the gateway guarantees an **Idempotency Replay** for the same operation, **Idempotency Key**, and request values. After this window, the same key may initiate a new payment command.
_Avoid_: Cache TTL, key lifetime, deduplication window

**Stuck Idempotency Claim**:
A public idempotency record still marked in progress long enough that the gateway treats the original command attempt as no longer active and may let the same operation, same **Idempotency Key**, and same request values continue recovery.
_Avoid_: Expired claim, stale payment, timeout

**Bank Operation Key**:
A gateway-generated operation identity sent to the **Mock Bank** so retried bank calls produce one bank result.
_Avoid_: Idempotency Key, request ID

## Relationships

- A **Payment** has exactly one **Payment ID**.
- A **Payment** references exactly one **Order ID**.
- A **Payment** references exactly one **Customer ID**.
- A **Payment** has exactly one **Amount**.
- A **Payment** has exactly one **Currency**.
- A **Payment** has exactly one **Payment Status**.
- Multiple **Payments** may reference the same **Order ID**.
- Public **Payment Status** values are `pending`, `authorized`, `expired`, `declined`, `captured`, `voided`, and `refunded`.
