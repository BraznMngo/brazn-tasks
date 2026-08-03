/**
 * One entry in a form's error summary: what is wrong, and which control to send
 * the reader to. See `components/misc/ErrorSummary.vue`.
 */
export interface IFieldError {
	/** The `id` of the control this entry moves focus to. */
	target: string;
	/** The field's own label, so the entry names the field it points at. */
	label: string;
	/** What is wrong with it. */
	message: string;
}
