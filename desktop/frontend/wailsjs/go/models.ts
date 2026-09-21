export namespace main {
	
	export class IssueVM {
	    id: number;
	    name: string;
	    publication: string;
	    coverUrl: string;
	    downloaded: boolean;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new IssueVM(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.publication = source["publication"];
	        this.coverUrl = source["coverUrl"];
	        this.downloaded = source["downloaded"];
	        this.path = source["path"];
	    }
	}
	export class UIConfig {
	    username: string;
	    hasPassword: boolean;
	    downloadDir: string;
	
	    static createFrom(source: any = {}) {
	        return new UIConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.username = source["username"];
	        this.hasPassword = source["hasPassword"];
	        this.downloadDir = source["downloadDir"];
	    }
	}

}

