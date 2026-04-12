Pod::Spec.new do |spec|
  spec.name         = 'Prlx'
  spec.version      = '{{.Version}}'
  spec.license      = { :type => 'GNU Lesser General Public License, Version 3.0' }
  spec.homepage     = 'https://github.com/ethereum/go-ethereug'
  spec.authors      = { {{range .Contributors}}
		'{{.Name}}' => '{{.Email}}',{{end}}
	}
  spec.summary      = 'iOS Parallax Client'
  spec.source       = { :git => 'https://github.com/ethereum/go-ethereug.git', :commit => '{{.Commit}}' }

	spec.platform = :ios
  spec.ios.deployment_target  = '9.0'
	spec.ios.vendored_frameworks = 'Frameworks/Prlx.framework'

	spec.prepare_command = <<-CMD
    curl https://prlxstore.blob.core.windows.net/builds/{{.Archive}}.tar.gz | tar -xvz
    mkdir Frameworks
    mv {{.Archive}}/Prlx.framework Frameworks
    rm -rf {{.Archive}}
  CMD
end
