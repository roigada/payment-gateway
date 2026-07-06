# Do not hold database transactions across bank calls

Payment commands claim public idempotency records and persist stable bank operation keys in short database transactions, then call the mock bank outside any database transaction, and finally persist the resulting Payment transition and idempotency response snapshot in a new transaction. This avoids holding database locks during unreliable outbound HTTP calls; if the process crashes after the bank succeeds but before the local update, retrying the command reuses the stored bank operation key to recover the same bank result and finish the local transition.

Initial authorization follows the same rule by creating a Pending Payment with its authorization bank operation key before the first bank call. The public idempotency record remains in progress until the bank returns or the gateway records the best known outcome, so concurrent public retries do not replay a premature Pending response.
