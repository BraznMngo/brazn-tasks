import AbstractService from './abstractService'
import {downloadBlob} from '../helpers/downloadBlob'

// Suggested filename for the user's download only — the import side detects the
// archive by its contents, not its name, so renaming this cannot break re-import.
const DOWNLOAD_NAME = 'brazn-tasks-export.zip'

export default class DataExportService extends AbstractService {
	request(password: string) {
		return this.post('/user/export/request', {password})
	}

	status() {
		return this.getM('/user/export')
	}
	
	async download(password: string) {
		const clear = this.setLoading()
		try {
			const url = await this.getBlobUrl('/user/export/download', 'POST', {password})
			downloadBlob(url, DOWNLOAD_NAME)
		} finally {
			clear()
		}
	}
}
