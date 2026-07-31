export function setTitle(title : undefined | string) {
	document.title = (typeof title === 'undefined' || title === '')
		? 'Brazn Tasks'
		: `${title} | Brazn Tasks`
}
